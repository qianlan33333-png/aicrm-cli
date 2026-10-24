package cliupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// ResolveTarget freezes an entry's actual file once. Installation never follows
// the entry again, even if another installer retargets a symlink while downloading.
func ResolveTarget(target string) (string, error) {
	var err error
	if target == "" {
		target, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	if _, err = regularTarget(target); err != nil {
		return "", err
	}
	return target, nil
}

// ReadVersion reads only the local CLI version, not its source/catalog identity.
// v0.3 has no version command; its explicit first migration is guarded by the
// pre-download file snapshot rather than a guessed version.
func ReadVersion(ctx context.Context, target string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, target, "version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.Env = append(os.Environ(), "AICRM_CLI_NO_UPDATE_NOTIFIER=1")
	if err := cmd.Run(); err != nil {
		return "", errors.New("target CLI version unavailable")
	}
	var value struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(out.Bytes(), &value) != nil {
		return "", errors.New("target CLI version unavailable")
	}
	if _, err := parseVersion(value.Version); err != nil {
		return "", errors.New("target CLI version unavailable")
	}
	return value.Version, nil
}

// VerifyInstalled is separate from byte installation so no business command is
// dispatched merely to check an update. A higher installed version is accepted.
func VerifyInstalled(ctx context.Context, result InstallResult) (InstallResult, error) {
	if result.Status == "downloaded_verified" {
		return result, nil
	}
	switch result.Status {
	case "installed", "already_installed", "up_to_date":
	default:
		return result, errors.New("update installation not confirmed")
	}
	version, err := ReadVersion(ctx, result.Target)
	if err == nil {
		var cmp int
		cmp, err = Compare(version, result.Version)
		if err == nil && cmp >= 0 {
			result.Version = version
			return result, nil
		}
	}
	result.Status = "installed_version_unverified"
	return result, errors.New("installed CLI version could not be confirmed; inspect the target before retrying")
}
