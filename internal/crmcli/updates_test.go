package crmcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	updater "github.com/qianlan33333-png/aicrm-cli/internal/crmcli/update"
)

func TestUpdateNoticeMachineAndExplicitCommands(t *testing.T) {
	for _, args := range [][]string{{"customers", "list"}, {"--profile", "local", "capabilities", "list"}, {"invoke", "customer.list"}, {"invoke", "customer.list", "--format", "table"}, {"invoke", "customer.list", "--input", "request.json"}} {
		if !noticeEligible(args, true, false) {
			t.Errorf("human invocation not eligible %v", args)
		}
		if noticeEligible(args, false, false) || noticeEligible(args, true, true) {
			t.Errorf("machine/disabled invocation prompted %v", args)
		}
	}
	for _, args := range [][]string{{"version"}, {"--version"}, {"update", "check"}, {"upgrade"}, {"auth", "login"}, {"invoke", "customer.list", "--format=json"}, {"invoke", "customer.list", "--format", "ndjson"}, {"invoke", "customer.list", "--input", "-"}, {"--help"}} {
		if noticeEligible(args, true, false) {
			t.Errorf("explicit/machine command prompted %v", args)
		}
	}
	var out, stderr bytes.Buffer
	if code := Run([]string{"version"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatalf("version exit %d", code)
	}
	var value map[string]any
	if e := json.Unmarshal(out.Bytes(), &value); e != nil || value["repository"] != updater.RepositoryURL || stderr.Len() != 0 {
		t.Fatalf("machine output changed: %s %s", out.String(), stderr.String())
	}
	out.Reset()
	stderr.Reset()
	if code := Run([]string{"unknown-command"}, strings.NewReader("yes\n"), &out, &stderr); code != 1 {
		t.Fatalf("business exit changed %d", code)
	}
	if !json.Valid(out.Bytes()) || stderr.Len() != 0 {
		t.Fatal("error output polluted by update notifier")
	}
}
func TestUpdateOfferRequiresChoiceAndKeepsFailureOnStderr(t *testing.T) {
	r := updater.Result{Current: "1.0.0", Latest: "v1.1.0", Available: true}
	calls := 0
	install := func() (updater.InstallResult, error) {
		calls++
		return updater.InstallResult{Download: "/tmp/verified.tar.gz"}, errors.New("atomic activation refused")
	}
	var stderr bytes.Buffer
	offerUpdate(strings.NewReader("\n"), &stderr, r, install)
	if calls != 0 {
		t.Fatal("default choice installed update")
	}
	stderr.Reset()
	offerUpdate(strings.NewReader("yes\n"), &stderr, r, install)
	if calls != 1 || !strings.Contains(stderr.String(), "atomic activation refused") || !strings.Contains(stderr.String(), "verified.tar.gz") {
		t.Fatal("explicit choice or failure evidence missing")
	}
}

func TestVersionIncludesExplicitBuildIdentity(t *testing.T) {
	previousVersion, previousClient, previousSource := Version, ClientCommit, CRMSourceCommit
	t.Cleanup(func() { Version, ClientCommit, CRMSourceCommit = previousVersion, previousClient, previousSource })
	Version, ClientCommit, CRMSourceCommit = "0.4.0-rc.2", strings.Repeat("a", 40), strings.Repeat("b", 40)
	v := versionInfo()
	if v["version"] != Version || v["client_commit"] != ClientCommit || v["crm_source_commit"] != CRMSourceCommit {
		t.Fatalf("build identity missing: %v", v)
	}
}
