package crmcli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
)

func TestAIProfileErrorReasonsRetainConflictExitAndSafeGuidance(t *testing.T) {
	op, ok := catalog.Find("admin.getAiProfile")
	if !ok {
		t.Fatal("profile operation missing")
	}
	for _, reason := range []string{"identity_pending", "identity_conflict"} {
		t.Run(reason, func(t *testing.T) {
			calls := 0
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != op.Binding.Path || r.URL.Query().Get("unionid") != "fixture-union" {
					t.Error("wrong profile request")
				}
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": reason, "error_detail": map[string]any{"version": 1, "reason_code": reason, "message": "PRIVATE source SQL fixture-union", "next_action": "resend"}})
			}))
			defer s.Close()
			c, err := NewClient(s.URL, Credential{Kind: "human", Token: "acl_fixture"}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			c.HTTP = s.Client()
			_, err = c.Call(context.Background(), op, CallInput{Query: map[string]string{"unionid": "fixture-union"}})
			var failure *Failure
			expected, known := catalog.FindErrorReason(reason)
			if !known || !errors.As(err, &failure) || failure.Code != "conflict" || failure.Exit != 5 || failure.Status != 409 || failure.ReasonCode != reason || failure.Message != expected.Message || failure.Detail == nil || failure.Detail.NextAction != expected.NextAction || calls != 1 {
				t.Fatalf("profile reason not safely retained: %+v", err)
			}
			encoded, _ := json.Marshal(failure)
			if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "fixture-union") || strings.Contains(string(encoded), "resend") {
				t.Fatal("CLI trusted unregistered server text")
			}
		})
	}
}

func TestDetailedErrorsRetainExitCategoriesAndNeverTrustServerText(t *testing.T) {
	for _, status := range []int{422, 503} {
		calls := 0
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("X-AICRM-Diagnostic-ID", strings.Repeat("a", 32))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"ok":false,"error":"legacy","error_detail":{"version":1,"reason_code":"material_title_too_long","message":"secret=fixture-secret <script>bad</script>","next_action":"resend","field":"recipients[0].content[1].title","actual":95,"limit":64,"unit":"utf8_bytes","diagnostic_id":"injected"}}`))
		}))
		c, err := NewClient(s.URL, Credential{}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c.HTTP = s.Client()
		_, err = c.request(context.Background(), "POST", "/create", "application/json", strings.NewReader(`{}`), "original-key")
		var failure *Failure
		if !errors.As(err, &failure) || failure.ReasonCode != "material_title_too_long" || failure.DiagnosticID != strings.Repeat("a", 32) || failure.Detail == nil {
			t.Fatalf("%+v", err)
		}
		if status == 422 && (failure.Exit != 2 || failure.Code != "validation") {
			t.Fatal("validation exit changed")
		}
		if status == 503 && (failure.Exit != 7 || failure.Code != "outcome_unknown" || failure.Detail.NextAction != "read_original") {
			t.Fatal("ambiguous write lost readback requirement")
		}
		data, _ := json.Marshal(failure)
		if strings.Contains(string(data), "fixture-secret") || strings.Contains(string(data), "<script>") || strings.Contains(string(data), "injected") || calls != 1 {
			t.Fatal("unsafe error or write retry")
		}
		s.Close()
	}
}

func TestDetailedErrorsFallbackForOldMalformedUnknownAndOversizedResponses(t *testing.T) {
	for _, body := range []string{`{"error":"permission_denied"}`, `not json secret`, `{"error_detail":{"version":1,"reason_code":"unknown","message":"secret"}}`, `{"error_detail":{"version":1,"reason_code":"invalid_request"},"error_detail":{}}`, strings.Repeat("s", 4097)} {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); _, _ = w.Write([]byte(body)) }))
		c, _ := NewClient(s.URL, Credential{}, time.Second)
		c.HTTP = s.Client()
		_, err := c.request(context.Background(), "GET", "/read", "", nil, "")
		var failure *Failure
		if !errors.As(err, &failure) || failure.Exit != 4 || failure.ReasonCode != "" || failure.Detail != nil || strings.Contains(failure.Message, "secret") {
			t.Fatalf("unsafe fallback: %+v", err)
		}
		s.Close()
	}
}
