package cliupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Real child processes verify installed versions on each native OS, including
// Windows file sharing rules. Cross-compilation does not satisfy this test.
func nativeBinary(t *testing.T, version string) []byte {
	t.Helper()
	d := t.TempDir()
	src := filepath.Join(d, "main.go")
	binary := filepath.Join(d, "fixture.exe")
	source := `package main
import("encoding/json";"os";"bufio";"fmt")
var version string
func main(){if len(os.Args)>1 && os.Args[1]=="hold" {fmt.Println("ready");bufio.NewReader(os.Stdin).ReadString('\n');return};json.NewEncoder(os.Stdout).Encode(map[string]string{"version":version})}`
	if e := os.WriteFile(src, []byte(source), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", binary, src)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("native build: %s %v", out, e)
	}
	b, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func nativeArchive(t *testing.T, binary []byte) []byte {
	if runtime.GOOS != "windows" {
		return tarArchive(t, []struct {
			name, body string
			kind       byte
		}{{"aicrm-cli", string(binary), tar.TypeReg}})
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, e := z.Create("aicrm-cli.exe")
	if e != nil {
		t.Fatal(e)
	}
	f.Write(binary)
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestNativeTargetReplacementVersionRaceAndReadback(t *testing.T) {
	ctx := context.Background()
	old := nativeBinary(t, "1.0.0")
	next := nativeBinary(t, "1.2.0")
	higher := nativeBinary(t, "2.0.0")
	archive := nativeArchive(t, next)
	t.Run("actual_entry_and_rollback", func(t *testing.T) {
		r, o, target := installFixture(t, archive, runtime.GOOS)
		os.WriteFile(target, old, 0755)
		v, e := Install(ctx, r, o)
		if e == nil {
			v, e = VerifyInstalled(ctx, v)
		}
		if e != nil || v.Version != "1.2.0" {
			t.Fatalf("install/readback %+v %v", v, e)
		}
		state, e := Status(target)
		if e != nil || state["status"] != "installed" {
			t.Fatalf("status %v %v", state, e)
		}
		if _, e = Rollback(target); e != nil {
			t.Fatal(e)
		}
		if version, e := ReadVersion(ctx, target); e != nil || version != "1.0.0" {
			t.Fatalf("rollback %s %v", version, e)
		}
	})
	t.Run("slower_older_update_cannot_overwrite_newer", func(t *testing.T) {
		r, o, target := installFixture(t, archive, runtime.GOOS)
		os.WriteFile(target, old, 0755)
		newerRelease, newerOptions, _ := installFixtureVersion(t, nativeArchive(t, higher), runtime.GOOS, "v2.0.0")
		newerOptions.Executable = target
		transport := o.Client.Transport
		started, continueDownload := make(chan struct{}), make(chan struct{})
		o.Client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.Path, "checksums.txt") {
				close(started)
				<-continueDownload
			}
			return transport.RoundTrip(req)
		})}
		finished := make(chan error, 1)
		go func() {
			<-started
			_, err := Install(ctx, newerRelease, newerOptions)
			close(continueDownload)
			finished <- err
		}()

		v, e := Install(ctx, r, o)
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if e == nil {
			v, e = VerifyInstalled(ctx, v)
		}
		if e != nil || v.Status != "up_to_date" || v.Version != "2.0.0" {
			t.Fatalf("version race %+v %v", v, e)
		}
		b, _ := os.ReadFile(target)
		if !bytes.Equal(b, higher) {
			t.Fatal("higher version overwritten")
		}
	})
	t.Run("changed_unknown_target_stops", func(t *testing.T) {
		r, o, target := installFixture(t, archive, runtime.GOOS)
		transport := o.Client.Transport
		o.Client = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			os.WriteFile(target, []byte("changed unknown"), 0755)
			return transport.RoundTrip(req)
		})}
		if _, e := Install(ctx, r, o); e == nil {
			t.Fatal("unknown target overwritten")
		}
		b, _ := os.ReadFile(target)
		if string(b) != "changed unknown" {
			t.Fatal("unknown target modified")
		}
	})
	t.Run("verified_download_then_external_replace", func(t *testing.T) {
		r, o, target := installFixture(t, archive, runtime.GOOS)
		os.WriteFile(target, old, 0755)
		o.DownloadOnly = true
		v, e := Install(ctx, r, o)
		if e != nil || v.Status != "downloaded_verified" || v.Target != target {
			t.Fatalf("download %+v %v", v, e)
		}
		version, _ := ReadVersion(ctx, target)
		if version != "1.0.0" {
			t.Fatal("download installed prematurely")
		}
		o.DownloadOnly = false
		v, e = Install(ctx, r, o)
		if e == nil {
			v, e = VerifyInstalled(ctx, v)
		}
		if e != nil || v.Version != "1.2.0" {
			t.Fatalf("external replacement %+v %v", v, e)
		}
	})
	for _, path := range filepath.SplitList(os.Getenv("AICRM_TEST_LEGACY_BINARIES")) {
		t.Run("real_legacy_"+filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			b, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			r, o, target := installFixture(t, archive, runtime.GOOS)
			os.WriteFile(target, b, 0755)
			v, e := Install(ctx, r, o)
			if e == nil {
				v, e = VerifyInstalled(ctx, v)
			}
			if e != nil || v.Version != "1.2.0" {
				t.Fatalf("legacy update %+v %v", v, e)
			}
		})
	}
}
func TestVerifyInstalledRejectsUnconfirmedVersion(t *testing.T) {
	_, _, target := installFixture(t, nil, "linux")
	v, e := VerifyInstalled(context.Background(), InstallResult{Status: "installed", Target: target, Version: "1.2.0"})
	if e == nil || v.Status != "installed_version_unverified" {
		t.Fatal(fmt.Sprint(v, e))
	}
}
