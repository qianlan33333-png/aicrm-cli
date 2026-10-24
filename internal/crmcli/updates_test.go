package crmcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestUpdateOfferReportsAutomaticBackupCleanup(t *testing.T) {
	for _, warning := range []string{"", "old backup could not be deleted"} {
		var stderr bytes.Buffer
		offerUpdate(strings.NewReader("yes\n"), &stderr, updater.Result{Latest: "v1.2.0"}, func() (updater.InstallResult, error) {
			return updater.InstallResult{Status: "installed", Version: "1.2.0", CleanupWarning: warning}, nil
		})
		if strings.Contains(stderr.String(), "回滚：") {
			t.Fatal("successful cleanup advertised rollback")
		}
		if warning == "" && !strings.Contains(stderr.String(), "自动删除") {
			t.Fatal("cleanup confirmation missing")
		}
		if warning != "" && (!strings.Contains(stderr.String(), warning) || strings.Contains(stderr.String(), "已自动删除")) {
			t.Fatal("cleanup warning hidden or falsely confirmed")
		}
	}
}

func TestCLIUpgradeAlreadyCurrentCleansPreviousReceipt(t *testing.T) {
	releasedVersion(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "version.go")
	target := filepath.Join(dir, "current-cli.exe")
	if e := os.WriteFile(source, []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Println(`{\"version\":\"1.0.0\"}`)}"), 0600); e != nil {
		t.Fatal(e)
	}
	if output, e := exec.Command("go", "build", "-o", target, source).CombinedOutput(); e != nil {
		t.Fatalf("fixture build %s %v", output, e)
	}
	current, _ := os.ReadFile(target)
	hash := sha256.Sum256(current)
	old := []byte("old version")
	oldHash := sha256.Sum256(old)
	backup := filepath.Join(dir, ".aicrm-cli-previous-fixture")
	os.WriteFile(backup, old, 0600)
	record := map[string]string{"Status": "installed", "Target": target, "Backup": backup, "OldSHA256": hex.EncodeToString(oldHash[:]), "NewSHA256": hex.EncodeToString(hash[:]), "Version": "v1.0.0"}
	raw, _ := json.Marshal(record)
	os.WriteFile(target+".update.json", raw, 0600)
	calls := 0
	var out, stderr bytes.Buffer
	o := &Options{In: strings.NewReader(""), Out: &out, Err: &stderr, updateCheck: releaseChecker(t, "v1.0.0", &calls, false), updateExecutable: target}
	o.updateInstall = func(context.Context, updater.Release, updater.InstallOptions) (updater.InstallResult, error) {
		t.Fatal("already current downloaded or reinstalled")
		return updater.InstallResult{}, nil
	}
	root := NewCommand(o)
	root.SetArgs([]string{"upgrade", "--download-only"})
	if e := root.Execute(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(backup); e != nil {
		t.Fatal("download-only command removed backup")
	}
	out.Reset()
	calls = 0
	root = NewCommand(o)
	root.SetArgs([]string{"upgrade"})
	if e := root.Execute(); e != nil {
		t.Fatal(e)
	}
	var result map[string]any
	if e := json.Unmarshal(out.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || result["status"] != "up_to_date" || result["rollback_available"] != false || result["channel"] != "stable" {
		t.Fatalf("result %s", out.Bytes())
	}
	if _, e := os.Stat(backup); !os.IsNotExist(e) {
		t.Fatal("previous updater backup remains")
	}
}
