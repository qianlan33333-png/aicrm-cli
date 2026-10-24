package cliupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func checksum(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func tarArchive(t *testing.T, entries []struct {
	name, body string
	kind       byte
}) []byte {
	t.Helper()
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	w := tar.NewWriter(g)
	for _, x := range entries {
		n := len(x.body)
		if x.kind == tar.TypeSymlink {
			n = 0
		}
		if e := w.WriteHeader(&tar.Header{Name: x.name, Size: int64(n), Mode: 0755, Typeflag: x.kind, Linkname: "/tmp/escape"}); e != nil {
			t.Fatal(e)
		}
		if n > 0 {
			if _, e := w.Write([]byte(x.body)); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if e := g.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func installFixture(t *testing.T, payload []byte, system string) (Release, InstallOptions, string) {
	t.Helper()
	return installFixtureVersion(t, payload, system, "v1.2.0")
}
func installFixtureVersion(t *testing.T, payload []byte, system, tag string) (Release, InstallOptions, string) {
	t.Helper()
	r := release(tag)
	name := archiveName(r, system, "amd64")
	sums := []byte(checksum(payload) + "  " + name + "\n")
	urlFor := func(n string) string { return RepositoryURL + "/releases/download/" + r.Tag + "/" + n }
	r.Assets = []Asset{{Name: name, URL: urlFor(name), Size: int64(len(payload)), Digest: "sha256:" + checksum(payload)}, {Name: "checksums.txt", URL: urlFor("checksums.txt"), Size: int64(len(sums)), Digest: "sha256:" + checksum(sums)}}
	h := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Error("credentials attached to release download")
		}
		var b []byte
		switch req.URL.String() {
		case urlFor(name):
			b = payload
		case urlFor("checksums.txt"):
			b = sums
		default:
			t.Errorf("untrusted request %s", req.URL)
			return nil, errors.New("unexpected URL")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b))}, nil
	})}
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(d, "aicrm-cli")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	if e := os.WriteFile(target, []byte("original executable"), 0755); e != nil {
		t.Fatal(e)
	}
	return r, InstallOptions{Client: h, Executable: target, CacheDir: t.TempDir(), GOOS: system, GOARCH: "amd64"}, target
}
func TestInstallVerifiedAtomicReadbackAndRollback(t *testing.T) {
	b := tarArchive(t, []struct {
		name, body string
		kind       byte
	}{{"docs/guide", "reference", tar.TypeReg}, {"aicrm-cli", "new executable", tar.TypeReg}})
	r, o, target := installFixture(t, b, "linux")
	result, e := Install(context.Background(), r, o)
	if e != nil || result.Status != "installed" || !result.RollbackAvailable || result.Download != "" {
		t.Fatalf("install %+v %v", result, e)
	}
	actual, _ := os.ReadFile(target)
	old, _ := os.ReadFile(result.Backup)
	if string(actual) != "new executable" || string(old) != "original executable" {
		t.Fatal("installation or backup bytes changed")
	}
	status, e := Status(target)
	if e != nil || status["status"] != "installed" {
		t.Fatalf("status %v %v", status, e)
	}
	back, e := Rollback(target)
	if e != nil || back["status"] != "rolled_back" {
		t.Fatalf("rollback %v %v", back, e)
	}
	actual, _ = os.ReadFile(target)
	if string(actual) != "original executable" {
		t.Fatal("original not restored")
	}
	if back, e = Rollback(target); e != nil || back["status"] != "already_rolled_back" {
		t.Fatal("repeated rollback changed result")
	}
}
func TestInstallFaultsPreserveOriginalAndUnknownTarget(t *testing.T) {
	b := tarArchive(t, []struct {
		name, body string
		kind       byte
	}{{"aicrm-cli", "new executable", tar.TypeReg}})
	t.Run("activation_failure", func(t *testing.T) {
		r, o, target := installFixture(t, b, "linux")
		o.Replace = func(string, string) error { return errors.New("disk fault") }
		v, e := Install(context.Background(), r, o)
		if e == nil || v.Status != "not_installed" || v.Download == "" {
			t.Fatalf("fault %+v %v", v, e)
		}
		actual, _ := os.ReadFile(target)
		if string(actual) != "original executable" {
			t.Fatal("activation error modified original")
		}
	})
	t.Run("bad_readback_recovers", func(t *testing.T) {
		r, o, target := installFixture(t, b, "linux")
		o.Replace = func(_, target string) error { return os.WriteFile(target, []byte("corrupt activation"), 0755) }
		v, e := Install(context.Background(), r, o)
		if e == nil || v.Status != "rolled_back" {
			t.Fatalf("fault %+v %v", v, e)
		}
		actual, _ := os.ReadFile(target)
		if string(actual) != "original executable" {
			t.Fatal("readback fault not recovered")
		}
	})
	t.Run("activation_error_after_success_reconciles_readback", func(t *testing.T) {
		r, o, _ := installFixture(t, b, "linux")
		o.Replace = func(source, target string) error {
			if e := replaceFile(source, target); e != nil {
				return e
			}
			return errors.New("reported error after activation")
		}
		v, e := Install(context.Background(), r, o)
		if e != nil || v.Status != "installed" {
			t.Fatalf("readback %+v %v", v, e)
		}
	})
	t.Run("activation_error_with_unknown_bytes_blocks_retry", func(t *testing.T) {
		r, o, target := installFixture(t, b, "linux")
		o.Replace = func(_, target string) error {
			if e := os.WriteFile(target, []byte("unknown installer bytes"), 0755); e != nil {
				return e
			}
			return errors.New("ambiguous filesystem result")
		}
		v, e := Install(context.Background(), r, o)
		if e == nil || v.Status != "outcome_unknown" {
			t.Fatalf("unknown %+v %v", v, e)
		}
		o.Replace = nil
		v, e = Install(context.Background(), r, o)
		if e == nil || v.Status != "outcome_unknown" {
			t.Fatalf("unsafe retry %+v %v", v, e)
		}
		actual, _ := os.ReadFile(target)
		if string(actual) != "unknown installer bytes" {
			t.Fatal("retry overwrote unknown target")
		}
		state, e := Status(target)
		if e != nil || state["status"] != "outcome_unknown" {
			t.Fatalf("status %v %v", state, e)
		}
	})
	t.Run("interrupted_prepared_receipt_reconciles_original", func(t *testing.T) {
		r, o, target := installFixture(t, b, "linux")
		old, _ := fileSHA(target)
		backup, e := tempCopy(target, filepath.Dir(target), ".aicrm-cli-previous-*", 0755)
		if e != nil {
			t.Fatal(e)
		}
		if e = atomicJSON(target+".update.json", installReceipt{Status: "prepared", Target: target, Backup: backup, OldSHA256: old, NewSHA256: checksum([]byte("new executable")), Version: r.Tag}); e != nil {
			t.Fatal(e)
		}
		v, e := Install(context.Background(), r, o)
		if e != nil || v.Status != "installed" {
			t.Fatalf("reconciled %+v %v", v, e)
		}
	})
	t.Run("unrelated_change_refuses_rollback", func(t *testing.T) {
		r, o, target := installFixture(t, b, "linux")
		if _, e := Install(context.Background(), r, o); e != nil {
			t.Fatal(e)
		}
		os.WriteFile(target, []byte("unrelated installer"), 0755)
		if _, e := Rollback(target); e == nil {
			t.Fatal("rollback overwrote unrelated version")
		}
	})
	t.Run("bad_backup_refuses_rollback", func(t *testing.T) {
		r, o, target := installFixture(t, b, "linux")
		v, e := Install(context.Background(), r, o)
		if e != nil {
			t.Fatal(e)
		}
		os.WriteFile(v.Backup, []byte("changed backup"), 0755)
		if _, e := Rollback(target); e == nil {
			t.Fatal("rollback used changed backup")
		}
	})
}

type interruptedBody struct{}

func (interruptedBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (interruptedBody) Close() error             { return nil }

func TestInstallDownloadInterruptionAndPermissionsPreserveExecutable(t *testing.T) {
	b := tarArchive(t, []struct {
		name, body string
		kind       byte
	}{{"aicrm-cli", "new executable", tar.TypeReg}})
	t.Run("interrupted_download", func(t *testing.T) {
		r, o, target := installFixture(t, b, "linux")
		original := o.Client.Transport
		o.Client.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
			resp, e := original.RoundTrip(req)
			if e == nil && strings.HasSuffix(req.URL.Path, ".tar.gz") {
				resp.Body = interruptedBody{}
			}
			return resp, e
		})
		v, e := Install(context.Background(), r, o)
		if e == nil || v.Status != "not_installed" {
			t.Fatalf("interruption %+v %v", v, e)
		}
		actual, _ := os.ReadFile(target)
		if string(actual) != "original executable" {
			t.Fatal("download interruption changed executable")
		}
		entries, _ := os.ReadDir(o.CacheDir)
		if len(entries) != 0 {
			t.Fatal("partial archive retained as verified")
		}
	})
	t.Run("readonly_installation_directory", func(t *testing.T) {
		r, o, target := installFixture(t, b, "linux")
		if runtime.GOOS == "windows" {
			// Windows ignores POSIX directory write bits. Inject the same OS
			// permission error; native executable sharing is exercised separately.
			o.Replace = func(string, string) error { return os.ErrPermission }
		} else {
			dir := filepath.Dir(target)
			if e := os.Chmod(dir, 0500); e != nil {
				t.Fatal(e)
			}
			defer os.Chmod(dir, 0700)
		}
		v, e := Install(context.Background(), r, o)
		if e == nil || v.Status != "not_installed" || v.Download == "" {
			t.Fatalf("permissions %+v %v", v, e)
		}
		actual, _ := os.ReadFile(target)
		if string(actual) != "original executable" {
			t.Fatal("permission failure changed executable")
		}
	})
	// checksums.txt remains mandatory even when older GitHub API responses omit
	// the optional per-asset digest field.
	t.Run("checksum_file_without_api_digest", func(t *testing.T) {
		r, o, _ := installFixture(t, b, "linux")
		for i := range r.Assets {
			r.Assets[i].Digest = ""
		}
		v, e := Install(context.Background(), r, o)
		if e != nil || v.Status != "installed" {
			t.Fatalf("checksums %+v %v", v, e)
		}
	})
}
func TestInstallRejectsChecksumUntrustedAndMaliciousArchive(t *testing.T) {
	for name, b := range map[string][]byte{
		"duplicate": tarArchive(t, []struct {
			name, body string
			kind       byte
		}{{"aicrm-cli", "one", tar.TypeReg}, {"aicrm-cli", "two", tar.TypeReg}}),
		"symlink": tarArchive(t, []struct {
			name, body string
			kind       byte
		}{{"aicrm-cli", "", tar.TypeSymlink}}),
		"traversal_only": tarArchive(t, []struct {
			name, body string
			kind       byte
		}{{"../aicrm-cli", "bad", tar.TypeReg}}),
		"not_archive": []byte("not gzip"),
	} {
		t.Run(name, func(t *testing.T) {
			r, o, target := installFixture(t, b, "linux")
			if _, e := Install(context.Background(), r, o); e == nil {
				t.Fatal("malicious archive installed")
			}
			actual, _ := os.ReadFile(target)
			if string(actual) != "original executable" {
				t.Fatal("rejection modified original")
			}
		})
	}
	b := tarArchive(t, []struct {
		name, body string
		kind       byte
	}{{"aicrm-cli", "new executable", tar.TypeReg}})
	t.Run("digest", func(t *testing.T) {
		r, o, target := installFixture(t, b, "linux")
		r.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
		if _, e := Install(context.Background(), r, o); e == nil {
			t.Fatal("digest mismatch accepted")
		}
		actual, _ := os.ReadFile(target)
		if string(actual) != "original executable" {
			t.Fatal("digest failure modified original")
		}
	})
	t.Run("source", func(t *testing.T) {
		r, o, _ := installFixture(t, b, "linux")
		r.Assets[1].URL = "https://evil.example/checksums.txt"
		if _, e := Install(context.Background(), r, o); e == nil {
			t.Fatal("untrusted source accepted")
		}
	})
	t.Run("duplicate_asset", func(t *testing.T) {
		r, o, _ := installFixture(t, b, "linux")
		r.Assets = append(r.Assets, r.Assets[0])
		if _, e := Install(context.Background(), r, o); e == nil {
			t.Fatal("ambiguous archive accepted")
		}
	})

}
func TestInstallWindowsVerifiedDownloadAndConcurrentLock(t *testing.T) {
	var payload bytes.Buffer
	z := zip.NewWriter(&payload)
	f, e := z.Create("aicrm-cli.exe")
	if e != nil {
		t.Fatal(e)
	}
	f.Write([]byte("windows binary"))
	z.Close()
	r, o, target := installFixture(t, payload.Bytes(), "windows")
	o.DownloadOnly = true
	v, e := Install(context.Background(), r, o)
	if e != nil || v.Status != "downloaded_verified" || v.Download == "" || v.RollbackAvailable {
		t.Fatalf("windows download %+v %v", v, e)
	}
	actual, _ := os.ReadFile(target)
	if string(actual) != "original executable" {
		t.Fatal("download-only modified installed binary")
	}
	b := tarArchive(t, []struct {
		name, body string
		kind       byte
	}{{"aicrm-cli", "new executable", tar.TypeReg}})
	r, o, target = installFixture(t, b, "linux")
	unlock, e := tryLock(target + ".update.lock")
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	if _, e := Install(context.Background(), r, o); !errors.Is(e, ErrBusy) {
		t.Fatalf("concurrent install not refused: %v", e)
	}
}
func TestDownloadDoesNotFollowUntrustedRedirect(t *testing.T) {
	r := release("v1.2.0")
	a := Asset{Name: "checksums.txt", URL: RepositoryURL + "/releases/download/v1.2.0/checksums.txt"}
	calls := 0
	h := apiClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.example/asset"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}, true)
	var out bytes.Buffer
	if _, e := download(context.Background(), h, r, a, &out, 128); e == nil || calls != 1 {
		t.Fatal("untrusted download redirect followed")
	}
}
