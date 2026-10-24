//go:build !windows

package cliupdate

import (
	"archive/tar"
	"context"
	"net/http"
	"os"
	"testing"
)

func TestInstallFreezesSymlinkTarget(t *testing.T) {

	archive := tarArchive(t, []struct {
		name, body string
		kind       byte
	}{{"aicrm-cli", "updated", tar.TypeReg}})
	r, o, target := installFixture(t, archive, "linux")
	other := target + ".other"
	os.WriteFile(other, []byte("other installation"), 0755)
	link := target + ".link"
	if e := os.Symlink(target, link); e != nil {
		t.Fatal(e)
	}
	o.Executable = link
	transport := o.Client.Transport
	retargeted := false
	o.Client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if !retargeted {
			retargeted = true
			os.Remove(link)
			if e := os.Symlink(other, link); e != nil {
				t.Fatal(e)
			}
		}
		return transport.RoundTrip(req)
	})}
	v, e := Install(context.Background(), r, o)
	if e != nil || v.Target != target {
		t.Fatalf("fixed target %+v %v", v, e)
	}
	a, _ := os.ReadFile(target)
	b, _ := os.ReadFile(other)
	dest, _ := os.Readlink(link)
	if string(a) != "updated" || string(b) != "other installation" || dest != other {
		t.Fatal("retargeted link followed or rewritten")
	}
}
