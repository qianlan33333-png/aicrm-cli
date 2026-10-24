package crmcli

import (
	"context"
	"fmt"
	updater "github.com/qianlan33333-png/aicrm-cli/internal/crmcli/update"
	"github.com/spf13/cobra"
	"runtime"
	"time"
)

func onlineCommand(cmd *cobra.Command) bool {
	top := cmd
	for top.Parent() != nil && top.Parent().Parent() != nil {
		top = top.Parent()
	}
	switch top.Name() {
	case "help", "version", "docs", "schema", "profile", "update", "upgrade", "completion", "__complete", "__completeNoDesc":
		return false
	case "capabilities":
		return cmd.Name() == "live"
	}
	return cmd.RunE != nil || cmd.Run != nil
}
func (o *Options) checker() updater.Checker {
	if o.updateCheck != nil {
		return *o.updateCheck
	}
	return updater.Checker{}
}
func (o *Options) installUpdate(ctx context.Context, r updater.Release, options updater.InstallOptions) (updater.InstallResult, error) {
	install := o.updateInstall
	if install == nil {
		install = updater.Install
	}
	return install(ctx, r, options)
}
func (o *Options) preflightUpdate(cmd *cobra.Command) error {
	if o.Context != "codex" || !onlineCommand(cmd) || clientVersion() == "dev" {
		return nil
	}
	latest, err := o.checker().Latest(cmd.Context(), clientVersion())
	if err != nil {
		fmt.Fprintln(o.Err, "未能检查 CLI 最新版本，继续使用当前版本。")
		return nil
	}
	if !latest.Available {
		return nil
	}
	system := o.updateGOOS
	if system == "" {
		system = runtime.GOOS
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()
	result, err := o.installUpdate(ctx, latest.Release, updater.InstallOptions{Executable: o.updateExecutable, DownloadOnly: system == "windows"})
	if err == nil {
		result, err = updater.VerifyInstalled(ctx, result)
	}
	message := "CLI update prepared; this business command was not executed. Codex may retry once after confirming the original entry's version; do not replay a pipeline or one-time stdin."
	if err != nil {
		message = "CLI update not confirmed; this business command was not executed. " + err.Error()
	}
	return &Failure{Code: "update_required", Exit: 9, Message: message, Result: struct {
		updater.InstallResult
		BusinessExecuted bool `json:"business_executed"`
	}{InstallResult: result}}
}
