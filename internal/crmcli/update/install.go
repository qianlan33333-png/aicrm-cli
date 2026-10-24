package cliupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Budgets bound disk use and archive expansion independently of reported sizes.
const archiveLimit int64 = 256 << 20
const binaryLimit int64 = 128 << 20

type InstallOptions struct {
	Client                             *http.Client
	CacheDir, Executable, GOOS, GOARCH string
	DownloadOnly                       bool
	// Replace is an internal injection point for exercising disk/activation faults.
	Replace func(string, string) error
}
type InstallResult struct {
	Status              string `json:"status"`
	Version             string `json:"version"`
	Target              string `json:"target,omitempty"`
	Download            string `json:"verified_archive,omitempty"`
	ArchiveSHA256       string `json:"archive_sha256"`
	BinarySHA256        string `json:"binary_sha256,omitempty"`
	Backup              string `json:"rollback_backup,omitempty"`
	RollbackAvailable   bool   `json:"rollback_available"`
	ServerCompatibility string `json:"server_compatibility"`
}
type installReceipt struct {
	Status, Target, Backup, OldSHA256, NewSHA256, Version string
}

func digestOK(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == sha256.Size && strings.ToLower(s) == s
}
func fileSHA(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func assetURL(r Release, a Asset) error {
	if _, e := parseVersion(r.Tag); e != nil {
		return e
	}
	want := RepositoryURL + "/releases/download/" + url.PathEscape(r.Tag) + "/" + url.PathEscape(a.Name)
	if a.URL != want || filepath.Base(a.Name) != a.Name {
		return errors.New("release asset is outside the trusted repository")
	}
	return nil
}
func download(ctx context.Context, h *http.Client, r Release, a Asset, dst io.Writer, limit int64) (string, error) {
	if e := assetURL(r, a); e != nil {
		return "", e
	}
	if a.Size > limit || a.Size < 0 {
		return "", errors.New("release asset exceeds download budget")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if e != nil {
		return "", e
	}
	req.Header.Set("User-Agent", "aicrm-cli-update")
	resp, e := h.Do(req)
	if e != nil {
		return "", errors.New("release download unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release download HTTP %d", resp.StatusCode)
	}
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return "", errors.New("release download interrupted")
	}
	if n > limit || a.Size > 0 && n != a.Size {
		return "", errors.New("release download size mismatch")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if a.Digest != "" && a.Digest != "sha256:"+digest {
		return "", errors.New("release asset digest mismatch")
	}
	return digest, nil
}
func assetNamed(r Release, name string) (Asset, error) {
	var found Asset
	count := 0
	for _, a := range r.Assets {
		if a.Name == name {
			found = a
			count++
		}
	}
	if count != 1 {
		return Asset{}, errors.New("release requires exactly one platform archive and checksum asset")
	}
	return found, nil
}
func expectedChecksum(raw, name string) (string, error) {
	digest := ""
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		if digest != "" || !digestOK(fields[0]) {
			return "", errors.New("release checksum invalid or ambiguous")
		}
		digest = fields[0]
	}
	if digest == "" {
		return "", errors.New("platform archive checksum missing")
	}
	return digest, nil
}
func archiveName(r Release, system, arch string) string {
	ext := ".tar.gz"
	if system == "windows" {
		ext = ".zip"
	}
	return "aicrm-cli_" + strings.TrimPrefix(r.Tag, "v") + "_" + system + "_" + arch + ext
}
func extractBinary(archive, system string, out *os.File) error {
	name := "aicrm-cli"
	if system == "windows" {
		name += ".exe"
	}
	found := false
	copyBinary := func(reader io.Reader, size int64, regular bool) error {
		if found || !regular || size <= 0 || size > binaryLimit {
			return errors.New("release binary invalid or duplicated")
		}
		found = true
		n, e := io.Copy(out, io.LimitReader(reader, binaryLimit+1))
		if e != nil || n != size {
			return errors.New("release binary unreadable")
		}
		return nil
	}
	if system == "windows" {
		z, e := zip.OpenReader(archive)
		if e != nil {
			return errors.New("release ZIP invalid")
		}
		defer z.Close()
		for _, f := range z.File {
			if f.Name != name {
				continue
			}
			if f.UncompressedSize64 > uint64(binaryLimit) {
				return errors.New("release binary exceeds expansion budget")
			}
			r, e := f.Open()
			if e != nil {
				return e
			}
			e = copyBinary(r, int64(f.UncompressedSize64), f.Mode().IsRegular())
			r.Close()
			if e != nil {
				return e
			}
		}
	} else {
		f, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		g, e := gzip.NewReader(f)
		if e != nil {
			return errors.New("release gzip invalid")
		}
		defer g.Close()
		r := tar.NewReader(io.LimitReader(g, 2*archiveLimit+1))
		for {
			h, e := r.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return errors.New("release tar invalid or expansion budget exceeded")
			}
			if h.Name != name {
				continue
			}
			if e = copyBinary(r, h.Size, h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA); e != nil {
				return e
			}
		}
	}
	if !found {
		return errors.New("release root binary missing")
	}
	return out.Sync()
}
func tempCopy(source, dir, pattern string, mode os.FileMode) (string, error) {
	s, e := os.Open(source)
	if e != nil {
		return "", e
	}
	defer s.Close()
	t, e := os.CreateTemp(dir, pattern)
	if e != nil {
		return "", e
	}
	p := t.Name()
	ok := false
	defer func() {
		t.Close()
		if !ok {
			os.Remove(p)
		}
	}()
	if e = t.Chmod(mode.Perm()); e != nil {
		return "", e
	}
	if _, e = io.Copy(t, s); e != nil {
		return "", e
	}
	if e = t.Sync(); e != nil {
		return "", e
	}
	if e = t.Close(); e != nil {
		return "", e
	}
	ok = true
	return p, nil
}
func regularTarget(path string) (os.FileInfo, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("upgrade target must be a regular executable, not a symlink")
	}
	return info, nil
}

func readReceipt(target string) (installReceipt, error) {
	var r installReceipt
	p := target + ".update.json"
	info, e := regularTarget(p)
	if e != nil {
		return r, e
	}
	if info.Size() > 64<<10 {
		return r, errors.New("upgrade receipt exceeds size budget")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(b, &r); e != nil || r.Target != target || filepath.Dir(r.Backup) != filepath.Dir(target) || !strings.HasPrefix(filepath.Base(r.Backup), ".aicrm-cli-previous-") || !digestOK(r.OldSHA256) || !digestOK(r.NewSHA256) {
		return r, errors.New("upgrade receipt invalid")
	}
	return r, nil
}

// Readback resolves a process interruption without assuming its last write
// completed. An unrelated binary or damaged receipt requires explicit repair.
func priorOutcomeKnown(target, actual string) error {
	r, e := readReceipt(target)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if actual != r.OldSHA256 && actual != r.NewSHA256 {
		return errors.New("previous upgrade outcome unknown; inspect upgrade status and backup before another write")
	}
	return nil
}

// Install never invokes the downloaded program or changes profiles/keyring.
// Windows activation may be refused by an OS file lock; the verified archive
// remains available for replacement after the running program has exited.
func Install(ctx context.Context, r Release, o InstallOptions) (result InstallResult, err error) {
	result = InstallResult{Version: r.Tag, Status: "not_installed", ServerCompatibility: "updater needs no new server API; business routes remain server-authorized"}
	if !releaseValid(r, "prerelease") {
		return result, errors.New("release identity invalid")
	}
	// Snapshot before the download. An unversioned v0.3 may be replaced only
	// if this exact file survives until we acquire the installation lock.
	entry := o.Executable
	if entry == "" {
		entry, _ = os.Executable()
	}
	result.Target, _ = filepath.Abs(entry)
	target, e := ResolveTarget(entry)
	if e != nil {
		return result, e
	}
	result.Target = target
	snapshot, e := fileSHA(target)
	if e != nil {
		return result, e
	}

	system, arch := o.GOOS, o.GOARCH
	if system == "" {
		system = runtime.GOOS
	}
	if arch == "" {
		arch = runtime.GOARCH
	}
	switch system + "/" + arch {
	case "darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64", "windows/amd64":
	default:
		return result, errors.New("no published archive for this platform")
	}
	asset, e := assetNamed(r, archiveName(r, system, arch))
	if e != nil {
		return result, e
	}
	checksums, e := assetNamed(r, "checksums.txt")
	if e != nil {
		return result, e
	}
	h := apiClient(o.Client, true)
	var sums strings.Builder
	if _, e = download(ctx, h, r, checksums, &sums, 256<<10); e != nil {
		return result, e
	}
	want, e := expectedChecksum(sums.String(), asset.Name)
	if e != nil {
		return result, e
	}
	d := o.CacheDir
	if d == "" {
		d, e = CacheDirectory()
		if e != nil {
			return result, e
		}
	}
	if e = os.MkdirAll(d, 0700); e != nil {
		return result, e
	}
	f, e := os.CreateTemp(d, asset.Name+"-*")
	if e != nil {
		return result, e
	}
	archive := f.Name()
	keep := false
	defer func() {
		f.Close()
		if !keep {
			os.Remove(archive)
		}
	}()
	digest, e := download(ctx, h, r, asset, f, archiveLimit)
	if e != nil {
		return result, e
	}
	if digest != want {
		return result, errors.New("release checksum mismatch")
	}
	if e = f.Sync(); e != nil {
		return result, e
	}
	if e = f.Close(); e != nil {
		return result, e
	}
	result.ArchiveSHA256 = digest
	result.Download = archive
	keep = true
	if o.DownloadOnly {
		result.Status = "downloaded_verified"
		return result, nil
	}
	info, e := regularTarget(target)
	if e != nil {
		return result, e
	}
	unlock, e := tryLock(target + ".update.lock")
	if e != nil {
		return result, e
	}
	defer unlock()
	// Re-read after locking: another upgrade may have completed while downloading.
	info, e = regularTarget(target)
	if e != nil {
		return result, e
	}
	old, e := fileSHA(target)
	if e != nil {
		return result, e
	}
	version, versionErr := ReadVersion(ctx, target)
	if versionErr == nil {
		cmp, compareErr := Compare(version, r.Tag)
		if compareErr != nil {
			return result, compareErr
		}
		if cmp >= 0 {
			result.Status = "up_to_date"
			result.Version = version
			result.BinarySHA256 = old
			os.Remove(archive)
			keep = false
			result.Download = ""
			return result, nil
		}
	} else if old != snapshot || o.Executable == "" {
		return result, errors.New("target changed while downloading and its version is unknown; no replacement performed")
	}
	if e = priorOutcomeKnown(target, old); e != nil {
		result.Status = "outcome_unknown"
		return result, e
	}
	newFile, e := os.CreateTemp(filepath.Dir(target), ".aicrm-cli-new-*")
	if e != nil {
		return result, fmt.Errorf("installation directory is not writable; verified archive retained: %w", e)
	}
	newPath := newFile.Name()
	defer os.Remove(newPath)
	if e = extractBinary(archive, system, newFile); e == nil {
		e = newFile.Chmod(info.Mode().Perm())
	}
	closeErr := newFile.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return result, e
	}
	newHash, e := fileSHA(newPath)
	if e != nil {
		return result, e
	}
	result.BinarySHA256 = newHash
	if old == newHash {
		result.Status = "already_installed"
		os.Remove(archive)
		keep = false
		result.Download = ""
		return result, nil
	}
	backup, e := tempCopy(target, filepath.Dir(target), ".aicrm-cli-previous-*", info.Mode())
	if e != nil {
		return result, e
	}
	result.Backup = backup
	result.RollbackAvailable = true
	receipt := installReceipt{Status: "prepared", Target: target, Backup: backup, OldSHA256: old, NewSHA256: newHash, Version: r.Tag}
	if e = atomicJSON(target+".update.json", receipt); e != nil {
		return result, e
	}
	// Refuse changes by unrelated installers; no blind overwrite of a new target.
	current, e := fileSHA(target)
	if e != nil || current != old {
		return result, errors.New("installed binary changed during upgrade; no replacement performed")
	}
	replace := o.Replace
	if replace == nil {
		replace = replaceFile
	}
	activationErr := replace(newPath, target)
	current, e = fileSHA(target)
	if activationErr != nil && (e != nil || current != newHash) {
		receipt.Status = "outcome_unknown"
		result.Status = "outcome_unknown"
		if e == nil && current == old {
			receipt.Status = "not_installed"
			result.Status = "not_installed"
		}
		_ = atomicJSON(target+".update.json", receipt)
		return result, errors.New("atomic activation failed; inspect upgrade status and retained backup (check permissions or running-file locks)")
	}
	if e != nil || current != newHash {
		restore, restoreErr := tempCopy(backup, filepath.Dir(target), ".aicrm-cli-restore-*", info.Mode())
		if restoreErr == nil {
			defer os.Remove(restore)
			restoreErr = replaceFile(restore, target)
		}
		if restoreErr != nil {
			result.Status = "outcome_unknown"
			receipt.Status = "outcome_unknown"
			_ = atomicJSON(target+".update.json", receipt)
			return result, errors.New("activation readback and automatic recovery failed; inspect original backup before another write")
		}
		restored, readErr := fileSHA(target)
		if readErr != nil || restored != old {
			result.Status = "outcome_unknown"
			receipt.Status = "outcome_unknown"
			_ = atomicJSON(target+".update.json", receipt)
			return result, errors.New("recovery readback failed; inspect original backup before another write")
		}
		result.Status = "rolled_back"
		receipt.Status = "rolled_back"
		_ = atomicJSON(target+".update.json", receipt)
		return result, errors.New("activation verification failed; original executable restored")
	}
	receipt.Status = "installed"
	if e = atomicJSON(target+".update.json", receipt); e != nil {
		result.Status = "installed_receipt_pending"
		return result, errors.New("binary installed and verified; receipt finalization failed, inspect upgrade status before another write")
	}
	result.Status = "installed"
	os.Remove(archive)
	keep = false
	result.Download = ""
	return result, nil
}
func Status(target string) (map[string]any, error) {
	target, e := ResolveTarget(target)
	if e != nil {
		return nil, e
	}
	r, e := readReceipt(target)
	if e != nil {
		return nil, e
	}
	actual, e := fileSHA(target)
	if e != nil {
		return nil, e
	}
	state := "outcome_unknown"
	if actual == r.NewSHA256 {
		state = "installed"
	}
	if actual == r.OldSHA256 {
		state = "original_binary"
	}
	return map[string]any{"status": state, "receipt_status": r.Status, "target": target, "binary_sha256": actual, "version": r.Version, "rollback_backup": r.Backup}, nil
}
func Rollback(target string) (map[string]any, error) {
	target, e := ResolveTarget(target)
	if e != nil {
		return nil, e
	}
	unlock, e := tryLock(target + ".update.lock")
	if e != nil {
		return nil, e
	}
	defer unlock()
	if _, e = regularTarget(target); e != nil {
		return nil, e
	}
	r, e := readReceipt(target)
	if e != nil {
		return nil, e
	}
	if _, e = regularTarget(r.Backup); e != nil {
		return nil, e
	}
	old, e := fileSHA(r.Backup)
	if e != nil || old != r.OldSHA256 {
		return nil, errors.New("rollback backup checksum mismatch")
	}
	current, e := fileSHA(target)
	if e != nil {
		return nil, e
	}
	if current == old {
		if r.Status != "rolled_back" {
			r.Status = "rolled_back"
			if e = atomicJSON(target+".update.json", r); e != nil {
				return nil, e
			}
		}
		return map[string]any{"status": "already_rolled_back", "target": target, "binary_sha256": old}, nil
	}
	if current != r.NewSHA256 {
		return nil, errors.New("current binary differs from original upgrade receipt; rollback refused")
	}
	info, e := os.Stat(r.Backup)
	if e != nil {
		return nil, e
	}
	tmp, e := tempCopy(r.Backup, filepath.Dir(target), ".aicrm-cli-rollback-*", info.Mode())
	if e != nil {
		return nil, e
	}
	defer os.Remove(tmp)
	if e = replaceFile(tmp, target); e != nil {
		return nil, e
	}
	actual, e := fileSHA(target)
	if e != nil || actual != old {
		return nil, errors.New("rollback readback failed; stop further writes")
	}
	r.Status = "rolled_back"
	if e = atomicJSON(target+".update.json", r); e != nil {
		return nil, e
	}
	return map[string]any{"status": "rolled_back", "target": target, "binary_sha256": old}, nil
}
