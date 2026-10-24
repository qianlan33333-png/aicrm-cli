package crmcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	updater "github.com/qianlan33333-png/aicrm-cli/internal/crmcli/update"
	"github.com/spf13/cobra"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type untouchedInput struct{ reads int }

func (i *untouchedInput) Read([]byte) (int, error) { i.reads++; return 0, io.EOF }
func releaseChecker(t *testing.T, tag string, calls *int, offline bool) *updater.Checker {
	return &updater.Checker{CacheDir: t.TempDir(), Client: &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		if r.Method != http.MethodHead || r.URL.String() != updater.RepositoryURL+"/releases/latest" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("wrong source or credentials")
		}
		if offline {
			return nil, errors.New("offline secret-must-not-escape")
		}
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{updater.RepositoryURL + "/releases/tag/" + tag}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
}
func releasedVersion(t *testing.T) {
	previous := Version
	Version = "1.0.0"
	t.Cleanup(func() { Version = previous })
}
func TestCLICodexPreflightClassifiesParsedCommand(t *testing.T) {
	releasedVersion(t)
	for _, tc := range []struct {
		args   []string
		checks int
	}{
		{[]string{"--context", "codex", "version"}, 0},
		{[]string{"--context", "codex", "--help"}, 0},
		{[]string{"--context", "codex", "profile", "list"}, 0},
		{[]string{"--context", "codex", "capabilities", "list"}, 0},
		{[]string{"--context", "codex", "schema"}, 0},
		{[]string{"--context", "codex", "probe", "--value", "version", "--format", "json"}, 1},
		{[]string{"--context", "codex", "probe", "--value", "auth", "--format", "ndjson"}, 1},
		{[]string{"probe", "--value", "version"}, 0},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			calls := 0
			var out, stderr bytes.Buffer
			o := &Options{In: strings.NewReader(""), Out: &out, Err: &stderr, updateCheck: releaseChecker(t, "v1.0.0", &calls, false)}
			root := NewCommand(o)
			probe := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
			probe.Flags().String("value", "", "")
			root.AddCommand(probe)
			root.SetArgs(tc.args)
			if e := root.Execute(); e != nil {
				t.Fatal(e)
			}
			if calls != tc.checks {
				t.Fatalf("checks=%d wanted=%d", calls, tc.checks)
			}
		})
	}
}
func TestCLICodexPreflightStopsBeforeBusinessAndInput(t *testing.T) {
	releasedVersion(t)
	for _, fault := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified_windows_archive", true: "installation_failure"}[fault], func(t *testing.T) {
			calls, installs, business := 0, 0, 0
			in := &untouchedInput{}
			var out, stderr bytes.Buffer
			o := &Options{In: in, Out: &out, Err: &stderr, updateGOOS: "windows", updateExecutable: "original-partner.exe", updateCheck: releaseChecker(t, "v1.2.0", &calls, false)}
			o.updateInstall = func(_ context.Context, r updater.Release, opt updater.InstallOptions) (updater.InstallResult, error) {
				installs++
				if r.Tag != "v1.2.0" || !opt.DownloadOnly || opt.Executable != "original-partner.exe" {
					t.Fatal("wrong installation target")
				}
				result := updater.InstallResult{Status: "downloaded_verified", Target: "original-partner.exe", Download: "verified.zip", Version: r.Tag}
				if fault {
					result.Status = "not_installed"
					return result, errors.New("disk failure")
				}
				return result, nil
			}
			root := NewCommand(o)
			root.AddCommand(&cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { business++; io.ReadAll(in); return nil }})
			root.SetArgs([]string{"--context", "codex", "probe"})
			var failure *Failure
			e := root.Execute()
			if !errors.As(e, &failure) || failure.Exit != 9 || failure.Code != "update_required" {
				t.Fatalf("wrong exit: %v", e)
			}
			b, _ := json.Marshal(failure.Result)
			var result map[string]any
			json.Unmarshal(b, &result)
			if calls != 1 || installs != 1 || business != 0 || in.reads != 0 || result["business_executed"] != false || result["target"] != "original-partner.exe" {
				t.Fatalf("business/input side effects: %s", b)
			}
		})
	}
}
func TestCLICodexPreflightFailOpenAndSingleDispatch(t *testing.T) {
	releasedVersion(t)
	for _, offline := range []bool{false, true} {
		calls, business := 0, 0
		var out, stderr bytes.Buffer
		o := &Options{In: strings.NewReader(""), Out: &out, Err: &stderr, updateCheck: releaseChecker(t, "v1.0.0", &calls, offline)}
		root := NewCommand(o)
		root.AddCommand(&cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { business++; return nil }})
		root.SetArgs([]string{"--context", "codex", "probe"})
		if e := root.Execute(); e != nil || calls != 1 || business != 1 {
			t.Fatalf("dispatch e=%v checks=%d business=%d", e, calls, business)
		}
		if strings.Contains(stderr.String(), "secret") || offline != (stderr.Len() > 0) || out.Len() != 0 {
			t.Fatal("failure notice polluted machine result or hid lookup failure")
		}
	}
}
func TestCLIUpgradeExplicitTargetEvenWhenBootstrapIsLatest(t *testing.T) {
	releasedVersion(t)
	calls, installs := 0, 0
	var out, stderr bytes.Buffer
	o := &Options{In: strings.NewReader(""), Out: &out, Err: &stderr, updateCheck: releaseChecker(t, "v1.0.0", &calls, false)}
	o.updateInstall = func(_ context.Context, _ updater.Release, opt updater.InstallOptions) (updater.InstallResult, error) {
		installs++
		if opt.Executable != "old-entry" || !opt.DownloadOnly {
			t.Fatal("target ignored")
		}
		return updater.InstallResult{Status: "downloaded_verified", Target: opt.Executable}, nil
	}
	root := NewCommand(o)
	root.SetArgs([]string{"upgrade", "--target", "old-entry", "--download-only"})
	if e := root.Execute(); e != nil || calls != 1 || installs != 1 {
		t.Fatalf("explicit target skipped %v %d %d", e, calls, installs)
	}
}

func TestCLICodexUpdateBeforeCredentialAndRequest(t *testing.T) {
	releasedVersion(t)
	for _, args := range [][]string{{"auth", "login", "--wecom", "--no-browser"}, {"auth", "login", "--username", "employee", "--secret-stdin"}, {"auth", "logout"}, {"doctor"}, {"invoke", "customer.detail.get", "--input", "-"}} {
		calls := 0
		in := &untouchedInput{}
		var out, stderr bytes.Buffer
		o := &Options{In: in, Out: &out, Err: &stderr, updateCheck: releaseChecker(t, "v1.2.0", &calls, false), updateGOOS: "windows", userConfigDir: func() (string, error) { t.Fatal("profile or credential accessed before update"); return "", nil }}
		o.updateInstall = func(context.Context, updater.Release, updater.InstallOptions) (updater.InstallResult, error) {
			return updater.InstallResult{Status: "downloaded_verified", Target: "partner.exe", Download: "verified.zip"}, nil
		}
		root := NewCommand(o)
		root.SetArgs(append([]string{"--context", "codex"}, args...))
		var failure *Failure
		if e := root.Execute(); !errors.As(e, &failure) || failure.Exit != 9 || calls != 1 || in.reads != 0 {
			t.Fatalf("side effect before update %v %d %d", e, calls, in.reads)
		}
	}
}

func TestCLIStableUpdateCommandsWithoutREST(t *testing.T) {
	releasedVersion(t)
	for _, args := range [][]string{{"update", "check", "--refresh"}, {"upgrade", "--download-only", "--target", "old-entry"}} {
		calls, installs := 0, 0
		var out, stderr bytes.Buffer
		o := &Options{In: strings.NewReader(""), Out: &out, Err: &stderr, updateCheck: releaseChecker(t, "v1.2.0", &calls, false)}
		o.updateInstall = func(_ context.Context, r updater.Release, opt updater.InstallOptions) (updater.InstallResult, error) {
			installs++
			if len(r.Assets) != 0 || r.Tag != "v1.2.0" || !opt.DownloadOnly {
				t.Fatal("REST metadata or wrong install")
			}
			return updater.InstallResult{Status: "downloaded_verified", Target: opt.Executable}, nil
		}
		root := NewCommand(o)
		root.SetArgs(args)
		if e := root.Execute(); e != nil || calls != 1 {
			t.Fatalf("stable command %v calls=%d", e, calls)
		}
		if (args[0] == "upgrade") != (installs == 1) {
			t.Fatal("wrong install dispatch")
		}
	}
}
func TestCLIPrereleaseRejectedBeforeInputAndInstall(t *testing.T) {
	releasedVersion(t)
	for _, args := range [][]string{{"update", "check", "--channel", "prerelease"}, {"upgrade", "--channel", "prerelease", "--target", "old-entry"}} {
		calls, installs := 0, 0
		var out, stderr bytes.Buffer
		in := &untouchedInput{}
		o := &Options{In: in, Out: &out, Err: &stderr, updateCheck: releaseChecker(t, "v1.2.0", &calls, false)}
		o.updateInstall = func(context.Context, updater.Release, updater.InstallOptions) (updater.InstallResult, error) {
			installs++
			return updater.InstallResult{}, nil
		}
		root := NewCommand(o)
		root.SetArgs(args)
		var failure *Failure
		if e := root.Execute(); !errors.As(e, &failure) || failure.Code != "update" || failure.Exit != 1 || !strings.Contains(failure.Message, "no longer supported") {
			t.Fatalf("channel error %v", e)
		}
		if calls != 0 || installs != 0 || in.reads != 0 {
			t.Fatal("disabled channel had side effects")
		}
	}
}
func TestCLICodexPreflightHTTPFailuresContinueOnce(t *testing.T) {
	releasedVersion(t)
	for _, status := range []int{200, 403, 429} {
		calls, business := 0, 0
		var out, stderr bytes.Buffer
		checker := &updater.Checker{Client: &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != http.MethodHead || r.URL.String() != updater.RepositoryURL+"/releases/latest" {
				t.Fatal("REST or fallback request")
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("invalid response"))}, nil
		})}}
		o := &Options{In: strings.NewReader(""), Out: &out, Err: &stderr, updateCheck: checker}
		root := NewCommand(o)
		root.AddCommand(&cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { business++; return nil }})
		root.SetArgs([]string{"--context", "codex", "probe"})
		if e := root.Execute(); e != nil || calls != 1 || business != 1 || stderr.Len() == 0 {
			t.Fatalf("failure handling %v calls=%d business=%d", e, calls, business)
		}
	}
}
