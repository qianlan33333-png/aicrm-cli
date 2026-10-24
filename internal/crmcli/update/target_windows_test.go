//go:build windows

package cliupdate

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestNativeWindowsBusyTargetThenExternalReplacement(t *testing.T) {

	old := nativeBinary(t, "1.0.0")
	next := nativeBinary(t, "1.2.0")
	r, o, target := installFixture(t, nativeArchive(t, next), runtime.GOOS)
	if e := os.WriteFile(target, old, 0755); e != nil {
		t.Fatal(e)
	}
	child := exec.Command(target, "hold")
	in, e := child.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := child.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { in.Close(); child.Process.Kill(); child.Wait() })
	var ready [6]byte
	if _, e = io.ReadFull(stdout, ready[:]); e != nil || string(ready[:]) != "ready\n" {
		t.Fatal("child not running")
	}
	v, e := Install(context.Background(), r, o)
	if e == nil || v.Status == "installed" || v.Download == "" {
		t.Fatalf("running Windows target falsely installed %+v %v", v, e)
	}
	if version, e := ReadVersion(context.Background(), target); e != nil || version != "1.0.0" {
		t.Fatalf("locked target damaged: %s %v", version, e)
	}
	in.Close()
	if e = child.Wait(); e != nil {
		t.Fatal(e)
	}
	v, e = Install(context.Background(), r, o)
	if e == nil {
		v, e = VerifyInstalled(context.Background(), v)
	}
	if e != nil || v.Version != "1.2.0" {
		t.Fatalf("external replacement after exit %+v %v", v, e)
	}
}
