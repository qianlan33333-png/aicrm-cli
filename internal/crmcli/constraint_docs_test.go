package crmcli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOfflineOperationGuidanceAndErrorSearchNeedNoCredentials(t *testing.T) {
	for _, args := range [][]string{{"docs", "operation", "admin.createAIAssistantPlan"}, {"docs", "search", "token_issuance_rate_limited"}} {
		var out, stderr bytes.Buffer
		if exit := Run(args, strings.NewReader(""), &out, &stderr); exit != 0 {
			t.Fatalf("exit=%d %s", exit, out.String())
		}
		var v map[string]any
		if json.Unmarshal(out.Bytes(), &v) != nil {
			t.Fatal(out.String())
		}
		if args[1] == "operation" {
			if v["constraints"] == nil || v["global_constraints"] == nil || v["error_reasons"] == nil {
				t.Fatal("missing guidance")
			}
		} else if !strings.Contains(out.String(), `"kind":"cli_error_reason"`) {
			t.Fatal("error reason missing from search")
		}
	}
}
