package crmcli

import (
	"os"
	"path/filepath"

	platformconfig "github.com/qianlan33333-png/aicrm-cli/internal/clientconfig"
)

// Omitted context preserves the original config, environment and keyring ABI.
// Profiles choose an origin; they are not separate identities within a space.
func (o *Options) configPath() (string, error) {
	if o.Context != "codex" {
		return configPath()
	}
	resolve := os.UserConfigDir
	if o.userConfigDir != nil {
		resolve = o.userConfigDir
	}
	dir, err := resolve()
	return filepath.Join(dir, "aicrm-cli-codex", "profiles.json"), err
}
func (o *Options) keyringService() string {
	if o.Context == "codex" {
		return "aicrm-cli-codex-v1"
	}
	return "aicrm-cli"
}
func (o *Options) authEnvironment() platformconfig.CLIEnvironment {
	if o.Context == "codex" {
		return platformconfig.CLIEnvironment{}
	}
	return platformconfig.LoadCLIEnvironment()
}

// Hold across backup, remote authorization, save, readback and rollback.
// Never remove lock files: waiters could otherwise lock a different inode.
func (o *Options) lockCredential(origin string) (func(), error) {
	if o.Context != "codex" {
		return func() {}, nil
	}
	path, err := o.configPath()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(filepath.Dir(path), "locks")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return lockCredentialFile(filepath.Join(dir, key(origin)+".lock"))
}
