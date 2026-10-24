//go:build legacy_compat

package crmcli

import (
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Real release executables live only in the isolated CI runner and are removed
// by its always-run cleanup. No user installation or keychain entry is touched.
func TestCLICompletionRealLegacyBinaryCompatibility(t *testing.T) {
	binaries := filepath.SplitList(os.Getenv("AICRM_TEST_LEGACY_BINARIES"))
	if len(binaries) == 0 {
		t.Fatal("isolated CI must supply real release binaries")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("legacy business compatibility fixture requires Linux")
	}
	reads := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acl_legacy_fixture" || r.Header.Get("Cookie") != "" {
			t.Error("legacy credential boundary changed")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/admin/access/users":
			reads++
			io.WriteString(w, `{"items":[{"id":7,"role":"viewer"}]}`)
		case "/api/admin/access/cli/session":
			io.WriteString(w, `{"kind":"human","actor_id":7}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, binary := range binaries {
		t.Run(filepath.Base(filepath.Dir(binary)), func(t *testing.T) {
			dir := t.TempDir()
			cert := filepath.Join(dir, "fixture.pem")
			os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
			credential := filepath.Join(dir, "credential.json")
			raw, _ := json.Marshal(Credential{Origin: server.URL, Kind: "human", Token: "acl_legacy_fixture", CSRF: "csrf_fixture"})
			os.WriteFile(credential, raw, 0600)
			env := append(os.Environ(), "AICRM_CLI_CONFIG_DIR="+filepath.Join(dir, "config"), "AICRM_CLI_TOKEN=", "AICRM_CLI_CREDENTIAL_FILE="+credential, "AICRM_CLI_NO_UPDATE_NOTIFIER=1", "SSL_CERT_FILE="+cert, "SSL_CERT_DIR="+dir)
			call := func(args ...string) []byte {
				t.Helper()
				cmd := exec.Command(binary, args...)
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("legacy %v failed: %v %s", args, err, out)
				}
				return out
			}
			call("profile", "add", "fixture", server.URL)
			if out := call("profile", "list"); !strings.Contains(string(out), server.URL) {
				t.Fatal("legacy configuration changed")
			}
			call("auth", "status")
			before := reads
			out := call("invoke", "admin.listAdminUsers")
			if reads != before+1 || !strings.Contains(string(out), `"id":7`) {
				t.Fatal("legacy business read did not execute once")
			}
			if !strings.Contains(filepath.Base(filepath.Dir(binary)), "0.3.") && !strings.Contains(filepath.Base(filepath.Dir(binary)), "0.4.") {
				return
			}
			cmd := exec.Command(binary, "--context", "codex", "auth", "status")
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err == nil && strings.Contains(string(out), "actor_id") {
				t.Fatal("legacy binary silently ignored context")
			}
		})
	}
}
