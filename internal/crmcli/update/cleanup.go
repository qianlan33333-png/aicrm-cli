package cliupdate

import (
	"errors"
	"os"
)

// cleanupConfirmedBackup runs only under the installation lock, after the
// caller has read the target's version and current bytes. It never scans a
// directory or removes a backup belonging to a different installation.
func cleanupConfirmedBackup(target, current string, remove func(string) error) (installReceipt, error) {
	r, err := readReceipt(target)
	if errors.Is(err, os.ErrNotExist) {
		return installReceipt{}, nil
	}
	if err != nil {
		return installReceipt{}, errors.New("installed CLI is confirmed, but its cleanup receipt could not be read")
	}
	if r.Status != "installed" || r.NewSHA256 != current {
		return r, errors.New("installed CLI is confirmed, but the cleanup receipt does not match; old files were retained")
	}
	if r.Backup == "" {
		return r, nil
	}
	if _, err = regularTarget(r.Backup); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return r, errors.New("old CLI backup is not a regular file; cleanup was refused")
		}
		hash, err := fileSHA(r.Backup)
		if err != nil || hash != r.OldSHA256 {
			return r, errors.New("old CLI backup changed; cleanup was refused")
		}
		if err = remove(r.Backup); err != nil {
			return r, errors.New("CLI upgraded successfully, but the old version backup could not be deleted")
		}
	}
	r.Backup = ""
	if err = atomicJSON(target+".update.json", r); err != nil {
		return r, errors.New("old CLI backup was deleted, but the cleanup receipt could not be saved")
	}
	return r, nil
}

func backupAvailable(r installReceipt) bool {
	if r.Backup == "" {
		return false
	}
	if _, err := regularTarget(r.Backup); err != nil {
		return false
	}
	hash, err := fileSHA(r.Backup)
	return err == nil && hash == r.OldSHA256
}
