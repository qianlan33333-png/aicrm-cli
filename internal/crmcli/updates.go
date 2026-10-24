package crmcli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
	updater "github.com/qianlan33333-png/aicrm-cli/internal/crmcli/update"
	platformconfig "github.com/qianlan33333-png/aicrm-cli/internal/clientconfig"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Release builds bind Version, ClientCommit and CRMSourceCommit. Development binaries do not
// query the public stable channel. Distribution-only docs/Webhook retain one
// extension point while the complete shared client remains V4-owned.
var Version = "dev"
var ClientCommit = ""
var CRMSourceCommit = ""
var distributionCommands []func(*cobra.Command, *Options)

func clientVersion() string {
	if _, e := updater.Compare(Version, Version); e == nil {
		return Version
	}
	if b, ok := debug.ReadBuildInfo(); ok {
		if _, e := updater.Compare(b.Main.Version, b.Main.Version); e == nil {
			return b.Main.Version
		}
	}
	return "dev"
}
func versionInfo() map[string]any {
	clientCommit := ClientCommit
	if b, ok := debug.ReadBuildInfo(); ok && clientCommit == "" {
		for _, s := range b.Settings {
			if s.Key == "vcs.revision" {
				clientCommit = s.Value
			}
		}
	}
	b, _ := json.Marshal(catalog.Load())
	h := sha256.Sum256(b)
	return map[string]any{"version": clientVersion(), "repository": updater.RepositoryURL, "client_commit": clientCommit, "crm_source_commit": CRMSourceCommit, "catalog_sha256": hex.EncodeToString(h[:]), "server_compatibility": "updater needs no new server API; each business route remains server-authorized"}
}
func updateFailure(e error, result any) error {
	return &Failure{Code: "update", Message: e.Error(), Exit: 1, Result: result}
}
func addUpdateCommands(root *cobra.Command, o *Options) {
	root.AddCommand(&cobra.Command{Use: "version", Args: cobra.NoArgs, Short: "client build and embedded contract identity", RunE: func(*cobra.Command, []string) error { return o.print(versionInfo()) }})
	channel := "stable"
	refresh := false
	check := &cobra.Command{Use: "check", Args: cobra.NoArgs, Short: "check a trusted release without changing the executable", RunE: func(cmd *cobra.Command, _ []string) error {
		r, e := o.checker().Check(cmd.Context(), clientVersion(), channel, refresh)
		if e != nil {
			return updateFailure(e, nil)
		}
		return o.print(r)
	}}
	check.Flags().StringVar(&channel, "channel", "stable", "stable; prerelease automatic selection is no longer supported")
	check.Flags().BoolVar(&refresh, "refresh", false, "refresh the public metadata cache")
	update := &cobra.Command{Use: "update", Short: "public release metadata"}
	update.AddCommand(check)
	root.AddCommand(update)
	upgradeChannel := "stable"
	downloadOnly := false
	target := ""
	upgrade := &cobra.Command{Use: "upgrade", Args: cobra.NoArgs, Short: "explicitly install a checksum-verified release; preserves rollback", RunE: func(cmd *cobra.Command, _ []string) error {
		r, e := o.checker().Check(cmd.Context(), clientVersion(), upgradeChannel, true)
		if e != nil {
			return updateFailure(e, nil)
		}
		if !r.Available && target == "" {
			return o.print(map[string]any{"status": "up_to_date", "version": clientVersion(), "channel": upgradeChannel})
		}
		v, e := o.installUpdate(cmd.Context(), r.Release, updater.InstallOptions{Executable: target, DownloadOnly: downloadOnly})
		if e == nil {
			v, e = updater.VerifyInstalled(cmd.Context(), v)
		}
		if e != nil {
			return updateFailure(e, v)
		}
		return o.print(v)
	}}
	upgrade.PersistentFlags().StringVar(&target, "target", "", "existing partner CLI entry to replace, inspect or roll back")
	upgrade.Flags().StringVar(&upgradeChannel, "channel", "stable", "stable; prerelease automatic selection is no longer supported")
	upgrade.Flags().BoolVar(&downloadOnly, "download-only", false, "retain a verified archive without installing (also for locked Windows executables)")
	upgrade.AddCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		r, e := updater.Status(target)
		if e != nil {
			return updateFailure(e, nil)
		}
		return o.print(r)
	}})
	upgrade.AddCommand(&cobra.Command{Use: "rollback", Args: cobra.NoArgs, Short: "restore the exact previous executable if current and backup digests match", RunE: func(*cobra.Command, []string) error {
		r, e := updater.Rollback(target)
		if e != nil {
			return updateFailure(e, nil)
		}
		return o.print(r)
	}})
	root.AddCommand(upgrade)
}
func noticeEligible(args []string, terminal bool, disabled bool) bool {
	if !terminal || disabled || len(args) == 0 {
		return false
	}
	for i, a := range args {
		if a == "version" || a == "--version" || a == "update" || a == "upgrade" || a == "auth" || a == "--help" || a == "-h" {
			return false
		}
		if a == "--format=json" || a == "--format=ndjson" || a == "--format" && i+1 < len(args) && args[i+1] != "table" {
			return false
		}
		if a == "--input=-" || a == "--input" && i+1 < len(args) && args[i+1] == "-" {
			return false
		}
	}
	return true
}
func maybeNotifyUpdate(args []string, in io.Reader, stdout, stderr io.Writer) {
	input, ok := in.(*os.File)
	if !ok {
		return
	}
	output, ok := stderr.(*os.File)
	if !ok {
		return
	}
	standardOutput, ok := stdout.(*os.File)
	if !ok {
		return
	}
	if !noticeEligible(args, term.IsTerminal(int(input.Fd())) && term.IsTerminal(int(output.Fd())) && term.IsTerminal(int(standardOutput.Fd())), platformconfig.LoadCLIEnvironment().NoUpdateNotifier) {
		return
	}
	v := clientVersion()
	if v == "dev" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), updater.CheckTimeout)
	defer cancel()
	r, e := (updater.Checker{}).Check(ctx, v, "stable", false)
	if e != nil || !r.Available || r.Cached {
		return
	}
	// Only the process which refreshed metadata offers an update. Other
	// invocations reuse the cache and never repeat an interactive prompt.
	offerUpdate(in, stderr, r, func() (updater.InstallResult, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return updater.Install(ctx, r.Release, updater.InstallOptions{})
	})
}
func offerUpdate(in io.Reader, stderr io.Writer, r updater.Result, install func() (updater.InstallResult, error)) {
	fmt.Fprintf(stderr, "\nCRM CLI %s 可用（当前 %s）。现在更新？[y/N] ", r.Latest, r.Current)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "是":
	default:
		return
	}
	result, e := install()
	if e != nil {
		fmt.Fprintf(stderr, "更新未完成：%s\n", e)
		if result.Download != "" {
			fmt.Fprintf(stderr, "已校验归档：%s\n", result.Download)
		}
		return
	}
	fmt.Fprintf(stderr, "CLI 更新结果：%s %s；回滚：aicrm-cli upgrade rollback\n", result.Status, result.Version)
}
