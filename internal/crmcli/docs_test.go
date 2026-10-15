package crmcli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
)

func TestOfflineDocumentationWorksWithoutProfileAndPreservesCoverageGaps(t *testing.T) {
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	for _, args := range [][]string{{"docs", "coverage"}, {"docs", "search", "userid"}, {"docs", "official", "92113"}, {"docs", "official", "92264"}, {"docs", "operation", "admin.setAccessCLIPolicy"}} {
		var out, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(""), &out, &stderr); code != 0 {
			t.Fatalf("%v: code=%d %s", args, code, &out)
		}
		var v map[string]any
		if json.Unmarshal(out.Bytes(), &v) != nil {
			t.Fatalf("invalid JSON: %s", &out)
		}
		if args[1] == "coverage" && (v["body_unavailable"].(float64) <= 0 || v["body_retrieved"].(float64) <= 0) {
			t.Fatal("partial official coverage must remain explicit")
		}
		if args[1] == "official" {
			d := v["document"].(map[string]any)
			if d["id"] != "92113" || d["source_url"] != "https://developer.work.weixin.qq.com/document/path/92113" || d["status"] != "official_body_retrieved" || len(d["tables"].([]any)) == 0 {
				t.Fatal("official alias lost source or fields")
			}
		}
	}
	loadReference()
	for _, doc := range referenceDocs {
		if doc.Status == "body_unavailable" && (len(doc.Tables) != 0 || len(doc.Examples) != 0) {
			t.Fatal("unavailable source must not contain invented semantics")
		}
	}
}

func TestOperationDocsResolveReferencesAndDoNotInventFieldMeaning(t *testing.T) {
	op, ok := catalog.Find("customer.detail.get")
	if !ok {
		t.Fatal("missing operation")
	}
	doc := operationDocumentation(op)
	if len(doc["referenced_definitions"].(map[string]any)) == 0 || len(doc["fields"].([]map[string]any)) == 0 {
		t.Fatal("referenced response contract missing")
	}
	op, _ = catalog.Find("admin.setAccessCLIPolicy")
	doc = operationDocumentation(op)
	found := false
	for _, row := range doc["fields"].([]map[string]any) {
		if row["path"] == "allowed_cidrs" && row["location"] == "request_body" {
			found = row["required_at_parent"] == true && strings.Contains(row["description"].(string), "24小时")
		}
	}
	if !found {
		t.Fatal("CLI policy field meaning or requirement lost")
	}
	for _, op := range catalog.Load().Operations {
		_ = operationDocumentation(op)
	}
}
