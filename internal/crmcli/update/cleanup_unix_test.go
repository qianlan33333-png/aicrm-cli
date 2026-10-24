//go:build !windows

package cliupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeCleanupPermissionFailureKeepsInstalledResult(t *testing.T) {
	old, next := nativeBinary(t, "1.0.0"), nativeBinary(t, "1.2.0")
	r, o, target := installFixture(t, nativeArchive(t, next), runtime.GOOS)
	os.WriteFile(target, old, 0755)
	v, e := Install(context.Background(), r, o)
	if e != nil {
		t.Fatal(e)
	}
	directory := filepath.Dir(target)
	if e = os.Chmod(directory, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(directory, 0700)
	got, e := VerifyInstalled(context.Background(), v)
	if e != nil || got.Status != "installed" || got.CleanupWarning == "" || !got.RollbackAvailable {
		t.Fatalf("cleanup failure confused with install failure: %+v %v", got, e)
	}
	if _, e = os.Stat(v.Backup); e != nil {
		t.Fatal("permission failure lost backup")
	}
	if e = os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	got, e = VerifyInstalled(context.Background(), v)
	if e != nil || got.CleanupWarning != "" || got.Backup != "" {
		t.Fatalf("cleanup retry %+v %v", got, e)
	}
}
