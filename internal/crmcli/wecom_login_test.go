package crmcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestCLIWeComLoginStorageReadbackAndRecovery(t *testing.T) {
	for _, contextName := range []string{"", "codex"} {
		t.Run("context="+contextName, func(t *testing.T) {
			for _, mode := range []string{"keyring", "private-file", "browser-fallback", "pending", "readback-failed", "readback-mismatch", "consumed", "lost-response", "save-collision", "close-failed", "cross-origin", "interrupted"} {
				// File export belongs exclusively to the unchanged legacy mode.
				if contextName == "codex" && (mode == "private-file" || mode == "save-collision") {
					continue
				}
				t.Run(mode, func(t *testing.T) {
					keyring.MockInit()
					t.Setenv("AICRM_CLI_TOKEN", "")
					t.Setenv("AICRM_CLI_CREDENTIAL_FILE", "")
					polls, closes, opens := 0, 0, 0
					file := ""
					if mode == "private-file" || mode == "save-collision" {
						file = filepath.Join(t.TempDir(), "credential.json")
					}
					var server *httptest.Server
					server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						switch r.URL.Path {
						case "/api/admin/access/cli/device-authorization":
							u := server.URL + "/cli/authorize?code=ABCDEFGH"
							if mode == "cross-origin" {
								u = "https://other.example/cli/authorize?code=ABCDEFGH"
							}
							json.NewEncoder(w).Encode(map[string]any{"device_code": "acd_fixture_private", "user_code": "ABCD-EFGH", "verification_uri": u, "expires_at": time.Now().Add(time.Minute), "interval": 1})
						case "/api/admin/access/cli/device-token":
							polls++
							var b map[string]string
							json.NewDecoder(r.Body).Decode(&b)
							if b["device_code"] != "acd_fixture_private" {
								t.Error("wrong claim proof")
							}
							if mode == "pending" && polls == 1 {
								json.NewEncoder(w).Encode(map[string]any{"state": "pending"})
								return
							}
							if mode == "consumed" {
								json.NewEncoder(w).Encode(map[string]any{"state": "consumed"})
								return
							}
							if mode == "lost-response" {
								h := w.(http.Hijacker)
								c, _, _ := h.Hijack()
								c.Close()
								return
							}
							if mode == "save-collision" {
								if e := os.WriteFile(file, []byte("existing file"), 0600); e != nil {
									t.Error(e)
								}
							}
							json.NewEncoder(w).Encode(map[string]any{"state": "issued", "access_token": "acl_fixture_private", "csrf_token": "ac_fixture_private", "expires_at": nil})
						case "/api/admin/access/cli/device-cancel":
							closes++
							if mode == "close-failed" {
								w.WriteHeader(503)
								return
							}
							json.NewEncoder(w).Encode(map[string]any{"state": "closed"})
						case "/api/admin/access/cli/session":
							if r.Header.Get("Authorization") != "Bearer acl_fixture_private" || r.Header.Get("Cookie") != "" {
								t.Error("incorrect credential transport")
							}
							if mode == "readback-failed" || mode == "close-failed" {
								w.WriteHeader(401)
								return
							}
							origin := server.URL
							if mode == "readback-mismatch" {
								origin = "https://other.example"
							}
							json.NewEncoder(w).Encode(map[string]any{"kind": "human", "actor_id": 7, "server_origin": origin})
						default:
							t.Errorf("unexpected request %s", r.URL.Path)
							w.WriteHeader(404)
						}
					}))
					defer server.Close()
					prior := Credential{Origin: server.URL, Kind: "human", Token: "acl_old", CSRF: "ac_old"}
					storage := &Options{Context: contextName}
					// An unrelated legacy identity must survive every Codex outcome.
					legacy := Credential{Origin: server.URL, Kind: "human", Token: "acl_legacy", CSRF: "ac_legacy"}
					if contextName == "codex" {
						if e := StoreCredential(legacy); e != nil {
							t.Fatal(e)
						}
					}
					if e := storage.storeCredential(prior); e != nil {
						t.Fatal(e)
					}
					c, e := NewClient(server.URL, Credential{}, time.Second*3)
					if e != nil {
						t.Fatal(e)
					}
					c.HTTP.Transport = server.Client().Transport
					var out, stderr bytes.Buffer
					loginContext, interrupt := context.WithCancel(context.Background())
					defer interrupt()
					o := Options{Context: contextName, Format: "json", Timeout: time.Second * 3, Out: &out, Err: &stderr, OpenBrowser: func(context.Context, string) error {
						opens++
						if mode == "interrupted" {
							interrupt()
						}
						if mode == "browser-fallback" {
							return errors.New("unavailable")
						}
						return nil
					}}
					noBrowser := mode != "browser-fallback" && mode != "interrupted"
					e = o.wecomLogin(loginContext, c, noBrowser, file)
					success := mode == "keyring" || mode == "private-file" || mode == "browser-fallback" || mode == "pending"
					if (e == nil) != success {
						t.Fatalf("unexpected result success=%t err=%v", success, e)
					}
					if success {
						if closes != 0 || !strings.Contains(out.String(), `"actor_id":7`) {
							t.Fatal("success/readback missing or successful login revoked")
						}
						if file != "" {
							info, err := os.Stat(file)
							if err != nil || info.Mode().Perm() != 0600 {
								t.Fatal("not a private file", err)
							}
						} else {
							saved, err := storage.loadCredential(server.URL)
							if err != nil || saved.Token != "acl_fixture_private" {
								t.Fatal("new credential not saved", err)
							}
						}
					} else {
						if closes != 1 {
							t.Fatalf("close calls %d", closes)
						}
						saved, err := storage.loadCredential(server.URL)
						if err != nil || saved != prior {
							t.Fatal("previous credential lost", err)
						}
						if mode == "close-failed" {
							var f *Failure
							if !errors.As(e, &f) || f.Code != "authorization_outcome_unknown" {
								t.Fatal("unknown revocation misreported")
							}
						}
					}
					if mode == "save-collision" {
						b, _ := os.ReadFile(file)
						if string(b) != "existing file" {
							t.Fatal("existing output file overwritten")
						}
					}
					if contextName == "codex" {
						got, err := LoadCredential(server.URL)
						if err != nil || got != legacy {
							t.Fatal("legacy credential changed", err)
						}
					}
					if noBrowser && opens != 0 {
						t.Fatal("--no-browser launched browser")
					}
					if mode == "browser-fallback" && !strings.Contains(stderr.String(), "复制") {
						t.Fatal("missing browser fallback")
					}
					for _, secret := range []string{"acd_fixture_private", "acl_fixture_private", "ac_fixture_private", "acl_old", "ac_old"} {
						if strings.Contains(out.String()+stderr.String(), secret) {
							t.Fatal("credential leaked to output")
						}
					}
					if mode != "pending" && polls > 1 {
						t.Fatal("unexpected automatic retry")
					}
				})
			}
		})
	}
}
func TestCLIWeComLoginFlagsDoNotChangeExistingModes(t *testing.T) {
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	if e := SaveConfig(Config{Current: "fixture", Profiles: map[string]Profile{"fixture": {Origin: "https://crm.example"}}}); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"--wecom", "--username", "someone"}, {"--wecom", "--client-id", "machine"}, {"--wecom", "--secret-stdin"}, {"--wecom", "--scope", "read"}, {"--no-browser"}} {
		var out, stderr bytes.Buffer
		o := &Options{Out: &out, Err: &stderr, In: strings.NewReader("")}
		cmd := NewCommand(o)
		cmd.SetArgs(append([]string{"auth", "login"}, args...))
		e := cmd.Execute()
		var f *Failure
		if !errors.As(e, &f) || f.Exit != 2 {
			t.Fatalf("args=%v err=%v", args, e)
		}
	}
}
