//go:build windows

package crmcli

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func lockCredentialFile(path string) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	var o windows.Overlapped
	if e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o); e != nil {
		f.Close()
		if errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
			return nil, fail("credential_busy", "another Codex login or logout is active for this origin; finish or interrupt that process first", 5)
		}
		return nil, e
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o); _ = f.Close() }, nil
}
