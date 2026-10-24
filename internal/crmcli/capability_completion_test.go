package crmcli

import (
	"bytes"
	"context"
	"errors"
	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
	"github.com/zalando/go-keyring"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func completionOperation(t *testing.T, id string) catalog.Operation {
	t.Helper()
	o, ok := catalog.Find("admin." + id)
	if !ok || o.Binding == nil {
		t.Fatal(id)
	}
	return o
}
func TestCLICompletionScopeCSRFAndUnknownOutcome(t *testing.T) {
	for _, tc := range []struct {
		id    string
		input CallInput
		scope string
		csrf  bool
		exit  int
	}{
		{"previewCustomerTagCommand", CallInput{Body: []byte(`{"customer_ids":[1],"add_tag_ids":[2],"idempotency_key":"existing-key-123"}`)}, "read", true, 1},
		{"queryServicePeriodMemberGrid", CallInput{Parameters: map[string]string{"service_product_id": "1"}, Body: []byte(`{"config":{"schema_version":1},"limit":10}`)}, "read", true, 1},
		{"getCouponShare", CallInput{Parameters: map[string]string{"coupon_id": "1"}}, "write", true, 7},
		{"downloadGroupInvitationQRCode", CallInput{Parameters: map[string]string{"id": "1"}}, "write", false, 7},
	} {
		t.Run(tc.id, func(t *testing.T) {
			op := completionOperation(t, tc.id)
			scope, ok := catalog.ManagedMachineScope(op)
			if !ok || scope != tc.scope {
				t.Fatalf("scope %s", scope)
			}
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if (r.Header.Get("X-CSRF-Token") == "csrf") != tc.csrf {
					t.Error("CSRF mismatch")
				}
				if r.Header.Get("X-AICRM-CLI-Operation") != op.ID || r.Header.Get("Cookie") != "" {
					t.Error("binding/credential mismatch")
				}
				w.WriteHeader(503)
				io.WriteString(w, `{"private":"DO-NOT-ECHO"}`)
			}))
			defer server.Close()
			c, _ := NewClient(server.URL, Credential{Kind: "human", Token: "acl_test", CSRF: "csrf"}, time.Second)
			c.HTTP.Transport = server.Client().Transport
			_, err := c.Call(context.Background(), op, tc.input)
			var f *Failure
			if !errors.As(err, &f) || f.Exit != tc.exit || calls != 1 || strings.Contains(err.Error(), "DO-NOT-ECHO") {
				t.Fatalf("calls=%d err=%+v", calls, err)
			}
		})
	}
}
func TestCLITagKeysNeverGuessOrConflict(t *testing.T) {
	for _, id := range []string{"acceptCustomerTagCommand", "previewCustomerTagCommand"} {
		op := completionOperation(t, id)
		for _, tc := range []struct {
			body, key string
			ok        bool
		}{
			{`{"customer_ids":[1],"add_tag_ids":[2],"idempotency_key":"body-existing-key"}`, "", true},
			{`{"customer_ids":[1],"add_tag_ids":[2]}`, "header-existing-key", true},
			{`{"customer_ids":[1],"add_tag_ids":[2],"idempotency_key":"body-existing-key"}`, "conflicting-key", false},
			{`{"customer_ids":[1],"add_tag_ids":[2]}`, "", false},
		} {
			_, _, _, err := BuildRequest(op, CallInput{Body: []byte(tc.body), IdempotencyKey: tc.key})
			if (err == nil) != tc.ok {
				t.Fatalf("%s %s err=%v", id, tc.body, err)
			}
		}
	}
}
func TestCLIInvitationSaveFailurePreservesOnlyConfirmedFacts(t *testing.T) {
	for _, tc := range []struct {
		body  string
		exit  int
		known bool
	}{
		{`{"error":"plan_save_failed","plan_id":23,"local_save_committed":true,"token":"PRIVATE"}`, 7, true},
		{`{"error":"plan_save_failed","plan_id":23}`, 5, false},
		{`{"plan_id":0,"local_save_committed":true}`, 5, false},
		{`{"plan_id":23,"local_save_committed":false}`, 5, false},
	} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(409); io.WriteString(w, tc.body) }))
		c, _ := NewClient(server.URL, Credential{Kind: "human", Token: "acl_test", CSRF: "csrf"}, time.Second)
		c.HTTP.Transport = server.Client().Transport
		_, err := c.Call(context.Background(), completionOperation(t, "saveGroupInvitationPlan"), CallInput{Body: []byte(`{"name":"Plan","title":"Join","mode":"single","chat_ids":["group-a"],"enabled":false}`), IdempotencyKey: "existing-invitation-key"})
		server.Close()
		var f *Failure
		if !errors.As(err, &f) || f.Exit != tc.exit || (f.Result != nil) != tc.known || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatalf("%+v", err)
		}
	}
}
func TestCLIQRDownloadReservesOutputBeforeSideEffect(t *testing.T) {
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "image/png")
		io.WriteString(w, "qr-bytes")
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = original }()
	if err := SaveConfig(Config{Current: "fixture", Profiles: map[string]Profile{"fixture": {Origin: server.URL}}}); err != nil {
		t.Fatal(err)
	}
	if err := StoreCredential(Credential{Origin: server.URL, Kind: "human", Token: "acl_test", CSRF: "csrf"}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	run := func(want int, args ...string) {
		t.Helper()
		out.Reset()
		stderr.Reset()
		code := Run(append([]string{"invoke", "admin.downloadGroupInvitationQRCode", "--param", "id=1"}, args...), strings.NewReader(""), &out, &stderr)
		if code != want {
			t.Fatalf("exit=%d %s", code, out.String())
		}
	}
	run(2)
	run(2, "--all")
	existing := filepath.Join(t.TempDir(), "existing.png")
	os.WriteFile(existing, []byte("original"), 0600)
	run(1, "--output", existing)
	if requests != 0 {
		t.Fatal("download was called before destination validation")
	}
	target := filepath.Join(t.TempDir(), "qr.png")
	run(0, "--output", target)
	body, _ := os.ReadFile(target)
	if requests != 1 || string(body) != "qr-bytes" {
		t.Fatal("download missing")
	}
}

func TestCLIWriteGETDisconnectIsNeverReplayed(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer server.Close()
	c, _ := NewClient(server.URL, Credential{Kind: "human", Token: "acl_fixture", CSRF: "csrf"}, time.Second)
	c.HTTP.Transport = server.Client().Transport
	_, err := c.Call(context.Background(), completionOperation(t, "getCouponShare"), CallInput{Parameters: map[string]string{"coupon_id": "1"}})
	var f *Failure
	if !errors.As(err, &f) || f.Exit != 7 || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestCLILegacyNativeReadScopeDoesNotChangePOSTFailure(t *testing.T) {
	op := testOperation("POST", "/open/v1/registered", "machine")
	op.Binding.Scope = "read"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	c, _ := NewClient(server.URL, Credential{Kind: "machine", Token: "fixture"}, time.Second)
	c.HTTP.Transport = server.Client().Transport
	_, err := c.Call(context.Background(), op, CallInput{})
	var f *Failure
	if !errors.As(err, &f) || f.Exit != 7 || op.Binding.EffectiveScope() != "write" {
		t.Fatalf("legacy native semantics changed: %v", err)
	}
}

func TestCLICompletionSurveyXLSXDownloadRetainsOwnerBytes(t *testing.T) {
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	t.Setenv("AICRM_CLI_CREDENTIAL_FILE", "")
	payload := []byte{'P', 'K', 3, 4, 0, 1, 2, 3}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/admin/questionnaires/7/export" || r.Header.Get("Authorization") != "Bearer acl_fixture" || r.Header.Get("X-AICRM-CLI-Operation") != "admin.exportSurveySubmissions" {
			t.Error("registered export or identity changed")
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="survey.xlsx"`)
		w.Write(payload)
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = original }()
	if err := SaveConfig(Config{Current: "fixture", Profiles: map[string]Profile{"fixture": {Origin: server.URL}}}); err != nil {
		t.Fatal(err)
	}
	if err := StoreCredential(Credential{Origin: server.URL, Kind: "human", Token: "acl_fixture", CSRF: "csrf"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "survey.xlsx")
	var out, stderr bytes.Buffer
	code := Run([]string{"invoke", "admin.exportSurveySubmissions", "--param", "questionnaire_id=7", "--output", path}, strings.NewReader(""), &out, &stderr)
	got, err := os.ReadFile(path)
	if code != 0 || err != nil || calls != 1 || !bytes.Equal(got, payload) {
		t.Fatalf("code=%d calls=%d err=%v out=%s", code, calls, err, out.String())
	}
	out.Reset()
	if code = Run([]string{"invoke", "admin.exportSurveySubmissions", "--param", "questionnaire_id=7", "--output", path}, strings.NewReader(""), &out, &stderr); code != 1 || calls != 1 {
		t.Fatal("existing export was overwritten or requested again")
	}
}
