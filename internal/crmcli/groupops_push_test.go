package crmcli

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

type pushFixture struct {
	groups        []planPushGroup
	reads, posts  int
	machineChecks int
	bodies        [][]byte
	events        []string
	planReads     int
	alter         func(*http.Request, map[string]any)
	postStatus    int
}

func newPushFixture(t *testing.T, count int) (*pushFixture, *httptest.Server) {
	t.Helper()
	fixture := &pushFixture{postStatus: 202}
	for i := 0; i < count; i++ {
		fixture.groups = append(fixture.groups, planPushGroup{Reference: fmt.Sprintf("bound-%04d", i)})
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open/v2/cli/context" {
			fixture.machineChecks++
			if r.Header.Get("Authorization") != "Bearer synthetic-machine-token" || r.Header.Get("Cookie") != "" || r.Header.Get("X-AICRM-Signature") != "" {
				t.Error("machine delegation probe mixed credentials")
			}
			io.WriteString(w, `{"admin_cli_authorized":false,"scopes":["read","write"]}`)
			return
		}
		if r.Method == "GET" {
			fixture.reads++
			if !strings.Contains(r.Header.Get("Cookie"), "human-fixture") || r.Header.Get("X-AICRM-Signature") != "" {
				t.Error("read credentials not isolated")
			}
			value := map[string]any{}
			if r.URL.Path == "/api/admin/automation-conversion/group-ops/plans/12" {
				fixture.planReads++
				value = map[string]any{"plan": map[string]any{"plan_id": "12", "plan_type": "webhook", "status": "active", "revision": 8}, "group_assets": fixture.groups, "webhook_descriptor": map[string]any{"reference": "fixture-hook", "configured": true}}
			} else if r.URL.Path == "/api/admin/automation-conversion/group-ops/plans/12/groups" {
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				if r.URL.Query().Get("limit") != "100" || offset > len(fixture.groups) {
					t.Error("wrong binding pagination")
				}
				end := min(offset+100, len(fixture.groups))
				value = map[string]any{"items": fixture.groups[offset:end], "offset": offset, "limit": 100, "total": len(fixture.groups), "has_more": end < len(fixture.groups)}
			} else {
				t.Error("read outside selected plan", r.URL.String())
			}
			if fixture.alter != nil {
				fixture.alter(r, value)
			}
			_ = json.NewEncoder(w).Encode(value)
			return
		}
		fixture.posts++
		raw, _ := io.ReadAll(r.Body)
		fixture.bodies = append(fixture.bodies, raw)
		fixture.events = append(fixture.events, r.Header.Get("X-AICRM-Event-Id"))
		if r.URL.Path != "/api/automation/group-ops/webhooks/fixture-hook" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-CSRF-Token") != "" {
			t.Error("write credentials or path mismatch")
		}
		mac := hmac.New(sha256.New, []byte(webhookFixtureSecret))
		mac.Write([]byte(r.Header.Get("X-AICRM-Timestamp") + "\n" + r.Header.Get("X-AICRM-Event-Id") + "\n"))
		mac.Write(raw)
		if r.Header.Get("X-AICRM-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("signature mismatch")
		}
		w.WriteHeader(fixture.postStatus)
		if fixture.postStatus == 202 {
			io.WriteString(w, `{"run":{"run_id":"901"},"executions":[]}`)
		} else {
			io.WriteString(w, `{"proxy_error":"unknown"}`)
		}
	}))
	t.Cleanup(server.Close)
	webhookTestProfile(t, server.URL)
	webhookTestTransport(t, server)
	if err := StoreCredential(Credential{Origin: server.URL, Kind: "human", Session: "human-fixture", CSRF: "csrf-fixture"}); err != nil {
		t.Fatal(err)
	}
	webhookTestConfigure(t)
	return fixture, server
}

func runPush(t *testing.T, event string, extra ...string) (int, map[string]any, string) {
	t.Helper()
	args := []string{"group-ops", "webhook", "push", "--plan-id", "12", "--text", "今晚一起看看知识库搭建的思路。", "--miniprogram-id", "1195", "--all-bound-groups"}
	if event != "" {
		args = append(args, "--event-id", event)
	}
	args = append(args, extra...)
	var out, stderr bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &stderr)
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(code, out.String(), stderr.String())
	}
	return code, result, stderr.String()
}

func TestPlanPushCompletePaginationExactTargetsAndCredentialIsolation(t *testing.T) {
	fixture, _ := newPushFixture(t, 105)
	code, result, stderr := runPush(t, webhookFixtureEvent)
	if code != 0 || result["target_count"] != float64(105) || fixture.posts != 1 || fixture.reads != 4 || result["acceptance"] != "accepted" || !strings.Contains(stderr, webhookFixtureEvent) {
		t.Fatal(code, result, fixture.reads, fixture.posts, stderr)
	}
	var body planPushBody
	if json.Unmarshal(fixture.bodies[0], &body) != nil || len(body.Targets) != 105 || len(body.Messages) != 2 || body.Messages[0].Type != "text" || body.Messages[1].MiniProgramID != 1195 {
		t.Fatal(body)
	}
	for i, target := range body.Targets {
		if target != fixture.groups[i].Reference {
			t.Fatal("incomplete target scope", i)
		}
	}
	path := result["request_snapshot"].(string)
	stat, err := os.Stat(path)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal(stat, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), webhookFixtureSecret) || strings.Contains(string(raw), "human-fixture") || strings.Contains(string(raw), "csrf-fixture") {
		t.Fatal("credential persisted")
	}
	snapshot, exists, err := loadPlanPushSnapshot(path)
	if err != nil || !exists || !bytes.Equal(snapshot.Request, fixture.bodies[0]) {
		t.Fatal("snapshot bytes differ", err)
	}
}

func TestPlanPushAutoEventDryRunAndFrozenSnapshotReplay(t *testing.T) {
	fixture, server := newPushFixture(t, 11)
	// A read-only preview never needs the HMAC key.
	keyring.Delete(webhookKeyringService, key(server.URL))
	code, preview, _ := runPush(t, "", "--dry-run")
	event, _ := preview["event_id"].(string)
	if code != 0 || !validWebhookEvent(event) || fixture.posts != 0 || fixture.reads != 3 || preview["write_request_sent"] != false {
		t.Fatal(code, preview, fixture)
	}
	webhookTestConfigure(t)
	// Changing live bindings must not change an old event's fixed request.
	fixture.groups = append(fixture.groups, planPushGroup{Reference: "new-bound-later"})
	code, result, _ := runPush(t, event)
	if code != 0 || result["snapshot_reused"] != true || fixture.reads != 3 || fixture.posts != 1 || result["target_count"] != float64(11) {
		t.Fatal(code, result, fixture)
	}
	code, _, _ = runPush(t, event)
	if code != 0 || fixture.posts != 2 || !bytes.Equal(fixture.bodies[0], fixture.bodies[1]) || fixture.events[0] != fixture.events[1] {
		t.Fatal("old event drifted", code, fixture)
	}
	code, conflict, _ := runPush(t, event, "--text", "另一段不同话术")
	if code != 5 || fixture.posts != 2 || conflict["error"].(map[string]any)["code"] != "idempotency_conflict" {
		t.Fatal(code, conflict)
	}
	code, newPreview, _ := runPush(t, "", "--dry-run")
	if code != 0 || newPreview["event_id"] == event || newPreview["target_count"] != float64(12) {
		t.Fatal(code, newPreview)
	}
}

func TestPlanPushRejectsIncompleteChangingAndInactiveBindingsWithoutPost(t *testing.T) {
	for _, name := range []string{"inactive", "standard-plan", "unconfigured", "empty", "duplicate", "wrong-total", "false-last-page", "wrong-offset", "changed-revision", "missing-plan-group", "outside-group"} {
		t.Run(name, func(t *testing.T) {
			fixture, _ := newPushFixture(t, 11)
			fixture.alter = func(r *http.Request, value map[string]any) {
				if strings.HasSuffix(r.URL.Path, "/groups") {
					switch name {
					case "empty":
						value["items"] = []planPushGroup{}
					case "duplicate":
						value["items"] = append([]planPushGroup{fixture.groups[0]}, fixture.groups[:10]...)
					case "wrong-total":
						value["total"] = 12
					case "false-last-page":
						value["has_more"] = true
					case "wrong-offset":
						value["offset"] = 1
					}
				} else {
					plan := value["plan"].(map[string]any)
					switch name {
					case "inactive":
						plan["status"] = "paused"
					case "standard-plan":
						plan["plan_type"] = "standard"
					case "unconfigured":
						value["webhook_descriptor"].(map[string]any)["configured"] = false
					case "changed-revision":
						if fixture.planReads == 2 {
							plan["revision"] = 9
						}
					case "missing-plan-group":
						value["group_assets"] = fixture.groups[:10]
					case "outside-group":
						groups := append([]planPushGroup(nil), fixture.groups...)
						groups[0].Reference = "outside-group"
						value["group_assets"] = groups
					}
				}
			}
			code, result, _ := runPush(t, webhookFixtureEvent)
			if code == 0 || fixture.posts != 0 {
				t.Fatal(code, result, fixture)
			}
		})
	}
}

func TestPlanPushRequiresExplicitScopeAndValidInputBeforeNetwork(t *testing.T) {
	fixture, _ := newPushFixture(t, 11)
	for _, extra := range [][]string{{"--all-bound-groups=false"}, {"--plan-id", "0"}, {"--miniprogram-id", "0"}, {"--text", " "}, {"--event-id", "short"}, {"--text", strings.Repeat("字", 1001)}} {
		code, result, _ := runPush(t, webhookFixtureEvent, extra...)
		if code != 2 || fixture.reads != 0 || fixture.posts != 0 {
			t.Fatal(code, result, fixture)
		}
	}
}

func TestPlanPushSnapshotFailureAndCorruptionNeverPost(t *testing.T) {
	t.Run("persist-failure", func(t *testing.T) {
		fixture, server := newPushFixture(t, 11)
		path, _ := planPushSnapshotPath(server.URL, webhookFixtureEvent)
		if err := os.WriteFile(filepath.Dir(path), []byte("blocked-directory"), 0600); err != nil {
			t.Fatal(err)
		}
		code, result, _ := runPush(t, webhookFixtureEvent)
		if code != 1 || fixture.posts != 0 {
			t.Fatal(code, result, fixture)
		}
	})
	for _, name := range []string{"partial", "symlink", "wrong-origin", "body-tamper"} {
		t.Run(name, func(t *testing.T) {
			fixture, server := newPushFixture(t, 11)
			code, result, _ := runPush(t, webhookFixtureEvent, "--dry-run")
			if code != 0 {
				t.Fatal(result)
			}
			path := result["request_snapshot"].(string)
			switch name {
			case "partial":
				os.WriteFile(path, []byte("{"), 0600)
			case "symlink":
				os.Remove(path)
				os.Symlink("missing-target", path)
			default:
				snapshot, _, _ := loadPlanPushSnapshot(path)
				if name == "wrong-origin" {
					snapshot.Origin = "https://other.invalid"
				} else {
					snapshot.Request = []byte(`{"webhook_reference":".."}`)
				}
				raw, _ := json.Marshal(snapshot)
				os.WriteFile(path, raw, 0600)
			}
			code, result, _ = runPush(t, webhookFixtureEvent)
			if code == 0 || fixture.posts != 0 || fixture.reads != 3 {
				t.Fatal(code, result, fixture, server.URL)
			}
		})
	}
}

func TestPlanPushUnknownPreservesEventAndRequestWithoutRetry(t *testing.T) {
	fixture, _ := newPushFixture(t, 11)
	fixture.postStatus = 502
	code, result, stderr := runPush(t, webhookFixtureEvent)
	detail := result["result"].(map[string]any)
	if code != 7 || fixture.posts != 1 || detail["event_id"] != webhookFixtureEvent || detail["target_count"] != float64(11) || detail["request_snapshot"] == nil || !strings.Contains(stderr, webhookFixtureEvent) {
		t.Fatal(code, result, fixture, stderr)
	}
	if _, exists, err := loadPlanPushSnapshot(detail["request_snapshot"].(string)); err != nil || !exists {
		t.Fatal(err)
	}
}

func TestPlanPushChangingLaterPageAndMissingHumanAuthorizationNeverPost(t *testing.T) {
	t.Run("later-page", func(t *testing.T) {
		fixture, _ := newPushFixture(t, 105)
		fixture.alter = func(r *http.Request, value map[string]any) {
			if strings.HasSuffix(r.URL.Path, "/groups") && r.URL.Query().Get("offset") == "100" {
				value["total"] = 106
			}
		}
		code, result, _ := runPush(t, webhookFixtureEvent)
		if code != 2 || fixture.posts != 0 || fixture.reads != 3 {
			t.Fatal(code, result, fixture)
		}
	})
	t.Run("machine-without-delegation", func(t *testing.T) {
		fixture, server := newPushFixture(t, 11)
		if err := StoreCredential(Credential{Origin: server.URL, Kind: "machine", Token: "synthetic-machine-token"}); err != nil {
			t.Fatal(err)
		}
		code, result, _ := runPush(t, webhookFixtureEvent)
		if code != 4 || fixture.posts != 0 || fixture.reads != 0 || fixture.machineChecks != 1 {
			t.Fatal(code, result, fixture)
		}
	})
}
