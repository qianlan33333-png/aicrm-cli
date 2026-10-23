//go:build desktop_keyring

package crmcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// Explicit opt-in lane. It uses only a fresh loopback origin's fixture slots,
// never a real CRM origin, and fails instead of skipping unavailable keyrings.
// Run this test alone so ordinary unit tests do not install the mock provider.
func TestCLICodexRealDesktopKeyringAndLegacyBinaries(t *testing.T) {
	binaries := filepath.SplitList(os.Getenv("AICRM_TEST_LEGACY_BINARIES"))
	if len(binaries) != 2 {
		t.Fatal("supply the verified v0.3.0 and v0.4.0 release binary paths")
	}
	configRoot := t.TempDir()
	t.Setenv("AICRM_CLI_CONFIG_DIR", filepath.Join(configRoot, "legacy"))
	t.Setenv("AICRM_CLI_TOKEN", "")
	t.Setenv("AICRM_CLI_CREDENTIAL_FILE", "")
	t.Setenv("AICRM_CLI_NO_UPDATE_NOTIFIER", "1")
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/access/cli/device-authorization":
			json.NewEncoder(w).Encode(map[string]any{"device_code": "acd_desktop_fixture", "user_code": "ABCD-EFGH", "verification_uri": server.URL + "/cli/authorize?code=ABCDEFGH", "expires_at": time.Now().Add(time.Minute), "interval": 1})
		case "/api/admin/access/cli/device-token":
			io.WriteString(w, `{"state":"issued","access_token":"acl_desktop_fixture","csrf_token":"csrf_desktop_fixture"}`)
		case "/api/admin/access/cli/session":
			if r.Header.Get("Authorization") != "Bearer acl_desktop_fixture" {
				t.Error("wrong real-keyring readback credential")
			}
			json.NewEncoder(w).Encode(map[string]any{"kind": "human", "actor_id": 77, "server_origin": server.URL})
		case "/api/admin/access/cli/logout":
			if r.Header.Get("Authorization") != "Bearer acl_desktop_fixture" {
				t.Error("wrong real-keyring revoked credential")
			}
			io.WriteString(w, `{}`)
		case "/api/admin/access/cli/device-cancel":
			io.WriteString(w, `{"state":"closed"}`)
		default:
			t.Error("unexpected fixture request")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	origin := server.URL
	legacy := &Options{}
	codex := &Options{Context: "codex", userConfigDir: func() (string, error) { return configRoot, nil }}
	for _, o := range []*Options{legacy, codex} {
		if _, err := keyring.Get(o.keyringService(), key(origin)); !errors.Is(err, keyring.ErrNotFound) {
			t.Fatal("fixture slot already exists or keyring unavailable")
		}
		defer func(o *Options) {
			err := o.deleteCredential(origin)
			if err != nil && !errors.Is(err, keyring.ErrNotFound) {
				t.Error("fixture keyring cleanup failed")
			}
		}(o)
		if err := o.saveConfig(Config{Current: "fixture", Profiles: map[string]Profile{"fixture": {Origin: origin}}}); err != nil {
			t.Fatal(err)
		}
	}
	originalConfig, _ := legacy.configPath()
	before, _ := os.ReadFile(originalConfig)
	oldCredential := Credential{Origin: origin, Kind: "human", Token: "acl_old_fixture", CSRF: "csrf_old_fixture"}
	if err := legacy.storeCredential(oldCredential); err != nil {
		t.Fatalf("real legacy store failed: %v", err)
	}
	prior := oldCredential
	prior.Token = "acl_codex_prior_fixture"
	if err := codex.storeCredential(prior); err != nil {
		t.Fatalf("real Codex store failed: %v", err)
	}
	c, _ := NewClient(origin, Credential{}, 3*time.Second)
	c.HTTP.Transport = server.Client().Transport
	var out, stderr bytes.Buffer
	codex.Format = "json"
	codex.Out = &out
	codex.Err = &stderr
	if err := codex.wecomLogin(context.Background(), c, true, ""); err != nil {
		t.Fatal(err)
	}
	saved, err := codex.loadCredential(origin)
	if err != nil || saved.Token != "acl_desktop_fixture" {
		t.Fatal("real Codex readback failed")
	}
	got, err := legacy.loadCredential(origin)
	if err != nil || got != oldCredential {
		t.Fatal("real legacy credential overwritten")
	}
	for _, binary := range binaries {
		cmd := exec.Command(binary, "profile", "list")
		raw, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(raw), origin) {
			t.Fatal("old binary config compatibility failed")
		}
		cmd = exec.Command(binary, "auth", "status")
		raw, err = cmd.CombinedOutput()
		// The unchanged release binary correctly loads the fixture identity, then
		// rejects the untrusted test TLS certificate. No trust-store changes needed.
		if err == nil || !strings.Contains(string(raw), `"code":"transport"`) {
			t.Fatal("old binary failed before legacy identity transport")
		}
		cmd = exec.Command(binary, "auth", "logout")
		raw, err = cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(raw), `"local_credential_removed":true`) {
			t.Fatal("old binary did not remove its own credential after unconfirmed revocation")
		}
		if _, err := legacy.loadCredential(origin); err == nil {
			t.Fatal("old binary retained legacy credential")
		}
		got, err := codex.loadCredential(origin)
		if err != nil || got != saved {
			t.Fatal("old binary removed Codex credential")
		}
		if err := legacy.storeCredential(oldCredential); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: original config and keyring load/delete preserved; fixture TLS rejection retained", filepath.Base(filepath.Dir(binary)))
	}
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = oldTransport }()
	out.Reset()
	logout := NewCommand(codex)
	logout.SetArgs([]string{"--context", "codex", "auth", "logout"})
	if err := logout.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err = legacy.loadCredential(origin)
	if err != nil || got != oldCredential {
		t.Fatal("real Codex logout removed legacy identity")
	}
	after, _ := os.ReadFile(originalConfig)
	if !bytes.Equal(before, after) {
		t.Fatal("old config changed")
	}
	t.Log("real desktop keyring: scan fixture save/readback/logout isolated; production WeCom not exercised")
}
