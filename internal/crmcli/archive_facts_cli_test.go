package crmcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
	"github.com/zalando/go-keyring"
)

func TestArchiveFactsCLISourceProcessStreamsLargeReadToFile(t *testing.T) {
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	t.Setenv("AICRM_CLI_PASSWORD", "")
	accounts := make([]string, 3145)
	for index := range accounts {
		accounts[index] = fmt.Sprintf("00000000-0000-4000-8000-%012x", index+1)
	}
	requestBody, err := json.Marshal(map[string]any{"hxc_account_ids": accounts})
	if err != nil || len(requestBody) <= 64<<10 {
		t.Fatalf("large request fixture bytes=%d err=%v", len(requestBody), err)
	}
	const responseBody = `{"data":{"contract":"crm-production-read-facts.v1","complete":true,"read_only":true}}`
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil || r.Method != http.MethodPost || r.URL.Path != "/open/v1/archive-facts" || len(body) <= 64<<10 || r.Header.Get("Authorization") != "Bearer ARCHIVE_READ_TOKEN" || r.Header.Get("Idempotency-Key") != "" {
			t.Errorf("unexpected archive CLI request method=%s path=%s bytes=%d", r.Method, r.URL.Path, len(body))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, responseBody)
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = original })
	if err = SaveConfig(Config{Current: "test", Profiles: map[string]Profile{"test": {Origin: server.URL}}}); err != nil {
		t.Fatal(err)
	}
	if err = StoreCredential(Credential{Origin: server.URL, Kind: "machine", Token: "ARCHIVE_READ_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	operation, found := catalog.Find("archive.facts.export")
	if !found || operation.Execution != "read" || operation.Binding == nil || operation.Binding.Capability != "archive.facts.read" || operation.Binding.Method != http.MethodPost || operation.Binding.Path != "/open/v1/archive-facts" || operation.Binding.IdempotencyRequired {
		t.Fatalf("archive facts CLI catalog binding=%+v found=%t", operation, found)
	}
	outputPath := filepath.Join(t.TempDir(), "archive-facts.json")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--timeout", "75s", "customers", "archive-facts", "export", "--input", "-", "--output", outputPath}, bytes.NewReader(requestBody), &stdout, &stderr)
	if code != 0 || requests != 1 {
		t.Fatalf("CLI exit=%d requests=%d stdout=%s stderr=%s", code, requests, stdout.String(), stderr.String())
	}
	saved, err := os.ReadFile(outputPath)
	if err != nil || string(saved) != responseBody {
		t.Fatalf("saved archive response bytes=%d err=%v", len(saved), err)
	}
	if !strings.Contains(stdout.String(), `"business_success":"consult_result"`) || strings.Contains(stdout.String(), "ARCHIVE_READ_TOKEN") {
		t.Fatalf("unexpected CLI receipt: %s", stdout.String())
	}
}
