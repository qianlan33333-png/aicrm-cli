//go:build !windows

package crmcli

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func lockCredentialFile(path string) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		if errors.Is(e, unix.EWOULDBLOCK) || errors.Is(e, unix.EAGAIN) {
			return nil, fail("credential_busy", "another Codex login or logout is active for this origin; finish or interrupt that process first", 5)
		}
		return nil, e
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}
