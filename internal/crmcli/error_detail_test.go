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
)

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
