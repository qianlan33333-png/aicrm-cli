package cliupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfirmedUpgradeDeletesOnlyItsBackup(t *testing.T) {
	old, next := nativeBinary(t, "1.0.0"), nativeBinary(t, "1.2.0")
	r, o, target := installFixture(t, nativeArchive(t, next), runtime.GOOS)
	os.WriteFile(target, old, 0755)
	v, e := Install(context.Background(), r, o)
	if e != nil {
		t.Fatal(e)
	}
	backup := v.Backup
	if _, e = os.Stat(backup); e != nil {
		t.Fatal("recovery copy absent before confirmation")
	}
	other := filepath.Join(filepath.Dir(target), ".aicrm-cli-previous-other-install")
	os.WriteFile(other, []byte("another install"), 0600)
	for range 2 {
		v, e = VerifyInstalled(context.Background(), v)
		if e != nil || v.Backup != "" || v.RollbackAvailable || v.CleanupWarning != "" {
			t.Fatalf("confirmation %+v %v", v, e)
		}
		if _, e = os.Stat(backup); !os.IsNotExist(e) {
			t.Fatal("old version remains")
		}
		if _, e = os.Stat(other); e != nil {
			t.Fatal("unrelated backup removed")
		}
		status, e := Status(target)
		if e != nil || status["status"] != "installed" || status["rollback_available"] != false || status["rollback_backup"] != "" {
			t.Fatalf("readback %v %v", status, e)
		}
	}
	if _, e = Rollback(target); e == nil || !strings.Contains(e.Error(), "automatically deleted") {
		t.Fatalf("rollback result %v", e)
	}
}
func TestCleanupRetainsUnconfirmedOrChangedFiles(t *testing.T) {
	old, next, higher := nativeBinary(t, "1.0.0"), nativeBinary(t, "1.2.0"), nativeBinary(t, "2.0.0")
	for _, kind := range []string{"unconfirmed_version", "changed_backup", "changed_target", "busy", "download_only", "missing_backup", "missing_receipt"} {
		t.Run(kind, func(t *testing.T) {
			payload := next
			if kind == "unconfirmed_version" {
				payload = []byte("not executable")
			}
			r, o, target := installFixture(t, nativeArchive(t, payload), runtime.GOOS)
			os.WriteFile(target, old, 0755)
			v, e := Install(context.Background(), r, o)
			if e != nil {
				t.Fatal(e)
			}
			backup := v.Backup
			switch kind {
			case "changed_backup":
				os.WriteFile(backup, []byte("foreign replacement"), 0600)
			case "changed_target":
				os.WriteFile(target, higher, 0755)
			case "missing_backup":
				os.Remove(backup)
			case "missing_receipt":
				os.Remove(target + ".update.json")
			case "download_only":
				r2, o2, _ := installFixtureVersion(t, nativeArchive(t, higher), runtime.GOOS, "v2.0.0")
				o2.Executable = target
				o2.DownloadOnly = true
				downloaded, e := Install(context.Background(), r2, o2)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = VerifyInstalled(context.Background(), downloaded); e != nil {
					t.Fatal(e)
				}
				if _, e = os.Stat(backup); e != nil {
					t.Fatal("archive preparation deleted recovery copy")
				}
				return
			case "busy":
				unlock, e := tryLock(target + ".update.lock")
				if e != nil {
					t.Fatal(e)
				}
				_, e = VerifyInstalled(context.Background(), v)
				unlock()
				if !errors.Is(e, ErrBusy) {
					t.Fatalf("concurrent confirmation ignored lock: %v", e)
				}
				if _, e = os.Stat(backup); e != nil {
					t.Fatal("busy operation removed backup")
				}
				return
			}
			got, e := VerifyInstalled(context.Background(), v)
			if kind == "unconfirmed_version" {
				if e == nil || got.Status != "installed_version_unverified" {
					t.Fatal("unconfirmed version accepted")
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if kind == "missing_backup" {
				if got.Backup != "" || got.CleanupWarning != "" {
					t.Fatal("missing backup not reconciled")
				}
				return
			}
			if _, e = os.Stat(backup); e != nil {
				t.Fatal("unconfirmed or foreign backup deleted")
			}
			if kind != "unconfirmed_version" && got.CleanupWarning == "" {
				t.Fatal("cleanup refusal hidden")
			}
		})
	}
}
func TestCleanupFailuresAreObservableAndRepeatable(t *testing.T) {
	old, next := nativeBinary(t, "1.0.0"), nativeBinary(t, "1.2.0")
	r, o, target := installFixture(t, nativeArchive(t, next), runtime.GOOS)
	os.WriteFile(target, old, 0755)
	v, e := Install(context.Background(), r, o)
	if e != nil {
		t.Fatal(e)
	}
	backup := v.Backup
	unlock, e := tryLock(target + ".update.lock")
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := cleanupConfirmedBackup(target, v.BinarySHA256, v.Version, func(string) error { return os.ErrPermission })
	unlock()
	if e == nil || !backupAvailable(receipt) {
		t.Fatal("cleanup failure lost recovery copy or error")
	}
	if _, e = os.Stat(backup); e != nil {
		t.Fatal(e)
	}
	v, e = VerifyInstalled(context.Background(), v)
	if e != nil || v.Backup != "" || v.CleanupWarning != "" {
		t.Fatalf("cleanup after permission release %+v %v", v, e)
	}
	// A crash after removal but before receipt update is reconciled by confirmation.
	r, o, target = installFixture(t, nativeArchive(t, next), runtime.GOOS)
	os.WriteFile(target, old, 0755)
	v, e = Install(context.Background(), r, o)
	if e != nil {
		t.Fatal(e)
	}
	receipt, e = cleanupConfirmedBackup(target, v.BinarySHA256, v.Version, func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := os.Remove(target + ".update.json"); err != nil {
			return err
		}
		return os.Mkdir(target+".update.json", 0700)
	})
	if e == nil || receipt.Backup != "" {
		t.Fatal("receipt write failure hidden")
	}
	if _, e = os.Stat(v.Backup); !os.IsNotExist(e) {
		t.Fatal("removed backup recreated")
	}
	actual, _ := fileSHA(target)
	if actual != v.BinarySHA256 {
		t.Fatal("cleanup failure changed installed binary")
	}
}
func TestNextUpgradeRetiresPriorSuccessAndReusesFailedBackup(t *testing.T) {
	old, next, higher := nativeBinary(t, "1.0.0"), nativeBinary(t, "1.2.0"), nativeBinary(t, "2.0.0")
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "old_successful_receipt", true: "failed_attempt_reuses_copy"}[failed], func(t *testing.T) {
			r, o, target := installFixture(t, nativeArchive(t, next), runtime.GOOS)
			os.WriteFile(target, old, 0755)
			if failed {
				o.Replace = func(string, string) error { return os.ErrPermission }
			}
			v, e := Install(context.Background(), r, o)
			if (e != nil) != failed {
				t.Fatal(e)
			}
			previous := v.Backup
			if failed {
				o.Replace = nil
			} else {
				r, o, _ = installFixtureVersion(t, nativeArchive(t, higher), runtime.GOOS, "v2.0.0")
				o.Executable = target
			}
			v, e = Install(context.Background(), r, o)
			if e != nil {
				t.Fatal(e)
			}
			if failed && v.Backup != previous {
				t.Fatal("failed retry orphaned prior backup")
			}
			if !failed {
				if _, e = os.Stat(previous); !os.IsNotExist(e) {
					t.Fatal("prior successful backup orphaned")
				}
			}
			v, e = VerifyInstalled(context.Background(), v)
			if e != nil || v.CleanupWarning != "" || v.RollbackAvailable {
				t.Fatalf("confirmation %+v %v", v, e)
			}
			backups, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".aicrm-cli-previous-*"))
			if len(backups) != 0 {
				t.Fatalf("old files remain: %v", backups)
			}
		})
	}
}

func TestNextUpgradeReconcilesInstalledBytesWithPendingReceipt(t *testing.T) {
	old, next, higher := nativeBinary(t, "1.0.0"), nativeBinary(t, "1.2.0"), nativeBinary(t, "2.0.0")
	for _, status := range []string{"prepared", "outcome_unknown", "not_installed"} {
		for _, replace := range []bool{false, true} {
			t.Run(status+map[bool]string{false: "_same_version", true: "_higher_version"}[replace], func(t *testing.T) {
				r, o, target := installFixture(t, nativeArchive(t, next), runtime.GOOS)
				os.WriteFile(target, old, 0755)
				v, e := Install(context.Background(), r, o)
				if e != nil {
					t.Fatal(e)
				}
				previous := v.Backup
				record, e := readReceipt(target)
				if e != nil {
					t.Fatal(e)
				}
				record.Status = status
				if e = atomicJSON(target+".update.json", record); e != nil {
					t.Fatal(e)
				}
				if replace {
					r, o, _ = installFixtureVersion(t, nativeArchive(t, higher), runtime.GOOS, "v2.0.0")
					o.Executable = target
					v, e = Install(context.Background(), r, o)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = os.Stat(previous); !os.IsNotExist(e) {
						t.Fatal("confirmed prior installation orphaned its recovery copy")
					}
				}
				v, e = VerifyInstalled(context.Background(), v)
				if e != nil || v.CleanupWarning != "" || v.RollbackAvailable {
					t.Fatalf("confirmation %+v %v", v, e)
				}
				backups, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".aicrm-cli-previous-*"))
				if len(backups) != 0 {
					t.Fatal("old recovery files remain")
				}
			})
		}
	}
}
func TestNextUpgradeRefusesUnconfirmedPriorVersion(t *testing.T) {
	old, wrong, higher := nativeBinary(t, "1.0.0"), nativeBinary(t, "0.9.0"), nativeBinary(t, "2.0.0")
	for _, payload := range [][]byte{wrong, []byte("version command unavailable")} {
		r, o, target := installFixture(t, nativeArchive(t, payload), runtime.GOOS)
		os.WriteFile(target, old, 0755)
		v, e := Install(context.Background(), r, o)
		if e != nil {
			t.Fatal(e)
		}
		previous := v.Backup
		if _, e = VerifyInstalled(context.Background(), v); e == nil {
			t.Fatal("wrong or unavailable installed version was confirmed")
		}
		r, o, _ = installFixtureVersion(t, nativeArchive(t, higher), runtime.GOOS, "v2.0.0")
		o.Executable = target
		if _, e = Install(context.Background(), r, o); e == nil {
			t.Fatal("unconfirmed previous install was overwritten")
		}
		if _, e = os.Stat(previous); e != nil {
			t.Fatal("unconfirmed recovery copy was removed")
		}
		current, _ := os.ReadFile(target)
		if string(current) != string(payload) {
			t.Fatal("unconfirmed installation bytes changed")
		}
	}
}
