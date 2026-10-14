package crmcli

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

const webhookFixtureSecret = "synthetic-groupops-key-32-bytes-long"
const webhookFixtureEvent = "groupops-fixture-event-0001"
const webhookFixtureBody = "{\n  \"webhook_reference\": \"fixture-hook\",\n  \"target_chat_references\": [\"bound-chat\"],\n  \"messages\": [{\"type\":\"text\",\"text\":\"今晚直播\\n资料已整理\"}]\n}\n"

func webhookTestProfile(t *testing.T, origin string) {
	t.Helper()
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	t.Setenv("AICRM_CLI_GROUP_OPS_WEBHOOK_SECRET", "")
	t.Setenv("AICRM_CLI_GROUP_OPS_WEBHOOK_ORIGIN", "")
	if err := SaveConfig(Config{Current: "fixture", Profiles: map[string]Profile{"fixture": {Origin: origin}}}); err != nil {
		t.Fatal(err)
	}
}

func webhookTestRun(args []string, input string) (int, string) {
	var out, stderr bytes.Buffer
	code := Run(args, strings.NewReader(input), &out, &stderr)
	return code, out.String() + stderr.String()
}

func webhookTestConfigure(t *testing.T) {
	t.Helper()
	if code, out := webhookTestRun([]string{"group-ops", "webhook", "configure", "--secret-stdin"}, webhookFixtureSecret+"\n"); code != 0 || strings.Contains(out, webhookFixtureSecret) {
		t.Fatal(code, out)
	}
}

func webhookTestTransport(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = original })
}

func webhookErrorBody(code string) string {
	return `{"ok":false,"error":{"code":"` + code + `"},"provider_execution_eligible":false,"real_external_call_executed":false}`
}

func TestWebhookCredentialLifecycleDoesNotReplaceLoginAndIsOriginBound(t *testing.T) {
	const origin = "https://origin-one.invalid"
	webhookTestProfile(t, origin)
	login := Credential{Origin: origin, Kind: "human", Session: "synthetic-existing-session", CSRF: "synthetic-csrf"}
	if err := StoreCredential(login); err != nil {
		t.Fatal(err)
	}
	webhookTestConfigure(t)
	if code, out := webhookTestRun([]string{"group-ops", "webhook", "status"}, ""); code != 0 || !strings.Contains(out, `"configured":true`) || strings.Contains(out, webhookFixtureSecret) {
		t.Fatal(code, out)
	}
	if actual, err := LoadCredential(origin); err != nil || actual != login {
		t.Fatal("webhook configure replaced existing login")
	}
	if err := SaveConfig(Config{Current: "other", Profiles: map[string]Profile{"fixture": {Origin: origin}, "other": {Origin: "https://origin-two.invalid"}}}); err != nil {
		t.Fatal(err)
	}
	if code, out := webhookTestRun([]string{"group-ops", "webhook", "status"}, ""); code != 0 || !strings.Contains(out, `"configured":false`) {
		t.Fatal("secret crossed origins", code, out)
	}
	if code, out := webhookTestRun([]string{"--profile", "fixture", "group-ops", "webhook", "forget"}, ""); code != 0 {
		t.Fatal(code, out)
	}
	if secret, _, err := loadWebhookSecret(origin); err != nil || secret != "" {
		t.Fatal("secret not removed")
	}
	if actual, err := LoadCredential(origin); err != nil || actual != login {
		t.Fatal("webhook forget changed existing login")
	}
	path, _ := configPath()
	if data, _ := os.ReadFile(path); strings.Contains(string(data), webhookFixtureSecret) {
		t.Fatal("secret persisted in profile file")
	}
}

func TestWebhookSignaturePreservesExactBytesAndOriginalEventReplay(t *testing.T) {
	calls, accepted := 0, 0
	events := map[string]string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/api/automation/group-ops/webhooks/fixture-hook" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-AICRM-Client-Id") != webhookClientID || !r.Close {
			t.Error("wrong protocol request")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-CSRF-Token") != "" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("human/machine credentials forwarded to signed protocol")
		}
		timestamp := r.Header.Get("X-AICRM-Timestamp")
		seconds, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil || time.Now().Unix()-seconds > 5 {
			t.Error("timestamp outside receiver window")
		}
		event := r.Header.Get("X-AICRM-Event-Id")
		mac := hmac.New(sha256.New, []byte(webhookFixtureSecret))
		mac.Write(append([]byte(timestamp+"\n"+event+"\n"), body...))
		if r.Header.Get("X-AICRM-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("receiver could not verify raw signed bytes")
		}
		w.Header().Set("Content-Type", "application/json")
		if previous, exists := events[event]; exists && previous != string(body) {
			w.WriteHeader(409)
			io.WriteString(w, webhookErrorBody("idempotency_conflict"))
			return
		}
		if _, exists := events[event]; !exists {
			accepted++
			events[event] = string(body)
		}
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]any{"run": map[string]any{"run_id": "41"}, "executions": []any{map[string]any{"execution_id": "77", "state": "accepted"}}, "untrusted_echo": []string{webhookFixtureSecret, r.Header.Get("X-AICRM-Signature")}})
	}))
	defer server.Close()
	webhookTestProfile(t, server.URL)
	webhookTestTransport(t, server)
	webhookTestConfigure(t)
	if err := StoreCredential(Credential{Origin: server.URL, Kind: "human", Session: "EXISTING_SESSION", CSRF: "EXISTING_CSRF"}); err != nil {
		t.Fatal(err)
	}
	args := []string{"group-ops", "webhook", "send", "--input", "-", "--event-id", webhookFixtureEvent}
	for attempt := 0; attempt < 2; attempt++ {
		code, out := webhookTestRun(args, webhookFixtureBody)
		if code != 0 || !strings.Contains(out, `"acceptance":"accepted"`) || !strings.Contains(out, `"delivery_proven":false`) || strings.Contains(out, webhookFixtureSecret) || strings.Contains(out, "sha256=") || strings.Contains(out, "EXISTING_SESSION") {
			t.Fatal(code, out)
		}
		var value map[string]any
		if json.Unmarshal([]byte(out), &value) != nil || value["request_digest"] != webhookDigest([]byte(webhookFixtureBody)) {
			t.Fatal("not one JSON result or digest changed")
		}
	}
	if events[webhookFixtureEvent] != webhookFixtureBody || accepted != 1 || calls != 2 {
		t.Fatal("raw bytes/event changed or replay created another logical run")
	}
	if code, out := webhookTestRun(args, strings.Replace(webhookFixtureBody, "今晚直播", "修改后的话术", 1)); code != 5 || !strings.Contains(out, "idempotency_conflict") || calls != 3 || accepted != 1 {
		t.Fatal(code, out)
	}
}

func TestWebhookDryRunAndInputRejectionsNeverDispatch(t *testing.T) {
	webhookTestProfile(t, "https://synthetic.invalid")
	// Deliberately malformed secret: dry-run must not even consult it.
	t.Setenv("AICRM_CLI_GROUP_OPS_WEBHOOK_SECRET", "not-a-valid-secret")
	cases := []struct{ name, body, event string }{
		{"duplicate_root", strings.Replace(webhookFixtureBody, `"webhook_reference":`, `"webhook_reference":"other","webhook_reference":`, 1), webhookFixtureEvent},
		{"unknown_root", strings.Replace(webhookFixtureBody, `"messages":`, `"url":"https://elsewhere.invalid","messages":`, 1), webhookFixtureEvent},
		{"path_escape", strings.Replace(webhookFixtureBody, "fixture-hook", "../escape", 1), webhookFixtureEvent},
		{"dot_segment", strings.Replace(webhookFixtureBody, "fixture-hook", "..", 1), webhookFixtureEvent},
		{"duplicate_target", strings.Replace(webhookFixtureBody, `["bound-chat"]`, `["bound-chat","bound-chat"]`, 1), webhookFixtureEvent},
		{"empty_target", strings.Replace(webhookFixtureBody, `["bound-chat"]`, `[]`, 1), webhookFixtureEvent},
		{"material_string", strings.Replace(webhookFixtureBody, `{"type":"text","text":"今晚直播\n资料已整理"}`, `{"type":"image","image_id":"12"}`, 1), webhookFixtureEvent},
		{"late_text", strings.Replace(webhookFixtureBody, `[{"type":"text"`, `[{"type":"image","image_id":12},{"type":"text"`, 1), webhookFixtureEvent},
		{"unknown_message_field", strings.Replace(webhookFixtureBody, `"type":"text"`, `"type":"text","image_id":0`, 1), webhookFixtureEvent},
		{"nul_text", strings.Replace(webhookFixtureBody, `今晚直播\n资料已整理`, `话术\u0000`, 1), webhookFixtureEvent},
		{"surrounding_whitespace", strings.Replace(webhookFixtureBody, "今晚直播", " 今晚直播", 1), webhookFixtureEvent},
		{"short_event", webhookFixtureBody, "short"},
		{"unicode_event", webhookFixtureBody, "groupops-中文-event-0001"},
		{"space_event", webhookFixtureBody, "groupops fixture event"},
		{"long_event", webhookFixtureBody, strings.Repeat("a", 257)},
		{"invalid_utf8", webhookFixtureBody + string([]byte{0xff}), webhookFixtureEvent},
		{"oversize", webhookFixtureBody + strings.Repeat(" ", webhookBodyLimit), webhookFixtureEvent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, out := webhookTestRun([]string{"group-ops", "webhook", "send", "--input", "-", "--event-id", tc.event, "--dry-run"}, tc.body); code != 2 {
				t.Fatal(code, out)
			}
		})
	}
	if code, out := webhookTestRun([]string{"group-ops", "webhook", "send", "--input", "-", "--event-id", webhookFixtureEvent, "--dry-run"}, webhookFixtureBody); code != 0 || !strings.Contains(out, `"network_request_sent":false`) || !strings.Contains(out, `"authorization_and_bound_targets_verified":false`) {
		t.Fatal(code, out)
	}
	if code, _ := webhookTestRun([]string{"group-ops", "webhook", "send", "--event-id", webhookFixtureEvent, "--dry-run"}, ""); code != 2 {
		t.Fatal("input flag not required")
	}
}

func TestWebhookAttachmentShapesAndFileInput(t *testing.T) {
	webhookTestProfile(t, "https://synthetic.invalid")
	for _, tc := range []struct {
		name, messages string
		code           int
	}{
		{"ordered", `[{"type":"text","text":"话术"},{"type":"image","image_id":7},{"type":"file","attachment_id":8},{"type":"miniprogram","miniprogram_id":9}]`, 0},
		{"dynamic_mini", `[{"type":"miniprogram","appid":"wx0ca836834b18e989","path":"pages/article/article?lesson_id=92ef53b5-9be3-4e9a-b5d1-f31b3e2a7837&from=learn","title":"今日课程"}]`, 0},
		{"mixed_mini_fields", `[{"type":"miniprogram","miniprogram_id":9,"appid":"wx-other"}]`, 2},
		{"two_mini", `[{"type":"miniprogram","miniprogram_id":9},{"type":"miniprogram","miniprogram_id":10}]`, 2},
		{"zero_id", `[{"type":"file","attachment_id":0}]`, 2},
		{"fraction_id", `[{"type":"image","image_id":1.5}]`, 2},
		{"overflow_id", `[{"type":"image","image_id":9223372036854775808}]`, 2},
		{"title_bytes", `[{"type":"miniprogram","appid":"wx-fixture","path":"page","title":"` + strings.Repeat("课", 22) + `"}]`, 2},
		{"empty", `[]`, 2},
		{"too_many", `[` + strings.Repeat(`{"type":"image","image_id":7},`, 10) + `{"type":"image","image_id":7}]`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "message.json")
			body := `{"webhook_reference":"fixture-hook","target_chat_references":["bound-chat"],"messages":` + tc.messages + `}`
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if code, out := webhookTestRun([]string{"group-ops", "webhook", "send", "--input", path, "--event-id", webhookFixtureEvent, "--dry-run"}, ""); code != tc.code {
				t.Fatal(code, out)
			}
		})
	}
}

func TestWebhookErrorsAndAmbiguousResponsesAreSafeAndNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		status, code int
	}{
		{"owner_validation", webhookErrorBody("invalid_request"), 400, 2},
		{"owner_authentication", webhookErrorBody("protocol_authentication_failed"), 401, 3},
		{"owner_not_found", webhookErrorBody("plan_not_found"), 404, 2},
		{"owner_conflict", webhookErrorBody("operations_conflict"), 409, 5},
		{"owner_disabled", webhookErrorBody("provider_disabled"), 503, 1},
		{"owner_cover_unavailable", webhookErrorBody("miniprogram_cover_unavailable"), 503, 1},
		{"untrusted_503", `{"error":"` + webhookFixtureSecret + `"}`, 503, 7},
		{"malformed_202", `{"run":`, 202, 7},
		{"missing_run_202", `{"ok":true}`, 202, 7},
		{"unexpected_success", `{"run":{"run_id":"41"}}`, 200, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			webhookTestProfile(t, server.URL)
			webhookTestTransport(t, server)
			webhookTestConfigure(t)
			code, out := webhookTestRun([]string{"group-ops", "webhook", "send", "--input", "-", "--event-id", webhookFixtureEvent}, webhookFixtureBody)
			if code != tc.code || calls != 1 || strings.Contains(out, webhookFixtureSecret) {
				t.Fatal(code, calls, out)
			}
			if tc.code == 7 && !strings.Contains(out, `"acceptance":"unknown"`) {
				t.Fatal("ambiguity reported as rejection or success", out)
			}
		})
	}
}

func TestWebhookConnectionLossAndSlowResponseDoNotReplay(t *testing.T) {
	for _, mode := range []string{"connection_loss", "slow_response_body"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				io.Copy(io.Discard, r.Body)
				if mode == "connection_loss" {
					connection, _, _ := w.(http.Hijacker).Hijack()
					connection.Close()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(202)
				io.WriteString(w, `{"run":`)
				w.(http.Flusher).Flush()
				time.Sleep(100 * time.Millisecond)
				io.WriteString(w, `{"run_id":"41"}}`)
			}))
			defer server.Close()
			webhookTestProfile(t, server.URL)
			webhookTestTransport(t, server)
			webhookTestConfigure(t)
			if code, out := webhookTestRun([]string{"group-ops", "webhook", "send", "--input", "-", "--event-id", webhookFixtureEvent, "--timeout", "20ms"}, webhookFixtureBody); code != 7 || calls != 1 || !strings.Contains(out, "outcome_unknown") {
				t.Fatal(code, calls, out)
			}
		})
	}
}

func TestWebhookRedirectNeverForwardsSigningCredentials(t *testing.T) {
	forwarded, initial := 0, 0
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded++ }))
	defer other.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		initial++
		http.Redirect(w, r, other.URL+"/receive", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	webhookTestProfile(t, server.URL)
	webhookTestTransport(t, server)
	webhookTestConfigure(t)
	if code, out := webhookTestRun([]string{"group-ops", "webhook", "send", "--input", "-", "--event-id", webhookFixtureEvent}, webhookFixtureBody); code != 4 || initial != 1 || forwarded != 0 || strings.Contains(out, webhookFixtureSecret) {
		t.Fatal(code, initial, forwarded, out)
	}
}

func TestWebhookEnvironmentBindingAndKeyringFailureDoNotLeak(t *testing.T) {
	const origin = "https://synthetic.invalid"
	webhookTestProfile(t, origin)
	t.Setenv("AICRM_CLI_GROUP_OPS_WEBHOOK_SECRET", webhookFixtureSecret)
	t.Setenv("AICRM_CLI_GROUP_OPS_WEBHOOK_ORIGIN", "https://wrong.invalid")
	if code, out := webhookTestRun([]string{"group-ops", "webhook", "send", "--input", "-", "--event-id", webhookFixtureEvent}, webhookFixtureBody); code != 3 || strings.Contains(out, webhookFixtureSecret) {
		t.Fatal(code, out)
	}
	t.Setenv("AICRM_CLI_GROUP_OPS_WEBHOOK_ORIGIN", origin)
	if code, out := webhookTestRun([]string{"group-ops", "webhook", "status"}, ""); code != 0 || !strings.Contains(out, `"credential_source":"environment"`) || strings.Contains(out, webhookFixtureSecret) {
		t.Fatal(code, out)
	}
	t.Setenv("AICRM_CLI_GROUP_OPS_WEBHOOK_SECRET", "")
	for _, secret := range []string{"short", " " + webhookFixtureSecret, strings.Repeat("a", 4097)} {
		if code, out := webhookTestRun([]string{"group-ops", "webhook", "configure", "--secret-stdin"}, secret); code != 2 || strings.Contains(out, webhookFixtureSecret) {
			t.Fatal(code, out)
		}
	}
	keyring.MockInitWithError(errors.New(webhookFixtureSecret))
	t.Cleanup(keyring.MockInit)
	for _, args := range [][]string{{"group-ops", "webhook", "configure", "--secret-stdin"}, {"group-ops", "webhook", "status"}, {"group-ops", "webhook", "forget"}} {
		if code, out := webhookTestRun(args, webhookFixtureSecret); code != 3 || strings.Contains(out, webhookFixtureSecret) {
			t.Fatal(code, out)
		}
	}
}

func TestWebhookSchemaIsDiscoverableWithoutSecretsOrNetwork(t *testing.T) {
	if code, out := webhookTestRun([]string{"schema", "webhook"}, ""); code != 0 || !strings.Contains(out, "GroupOpsWebhookInboundRequest") || !strings.Contains(out, "HMAC-SHA256") || strings.Contains(out, webhookFixtureSecret) {
		t.Fatal(code, out)
	}
}
