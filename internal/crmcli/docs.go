package crmcli

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
	"github.com/spf13/cobra"
)

// The reference is public source material, never a source of executable commands
// or credentials. It deliberately has no HTTP client or provider integration.
//
//go:embed reference/wecom.json
var officialReference []byte

type officialDocument struct {
	ID        string           `json:"id"`
	DocID     int              `json:"doc_id"`
	Title     string           `json:"title"`
	SourceURL string           `json:"source_url"`
	Status    string           `json:"status"`
	Aliases   []map[string]any `json:"aliases"`
	Tables    []map[string]any `json:"tables,omitempty"`
	Examples  []string         `json:"examples,omitempty"`
	Details   map[string]any   `json:"-"`
}

var referenceOnce sync.Once
var referenceDocs []officialDocument
var referenceCoverage map[string]any

func loadReference() {
	referenceOnce.Do(func() {
		var v struct {
			Coverage  map[string]any    `json:"coverage"`
			Documents []json.RawMessage `json:"documents"`
		}
		if json.Unmarshal(officialReference, &v) != nil {
			panic("invalid public documentation bundle")
		}
		referenceCoverage = v.Coverage
		for _, raw := range v.Documents {
			var doc officialDocument
			if json.Unmarshal(raw, &doc) != nil || json.Unmarshal(raw, &doc.Details) != nil {
				panic("invalid public documentation record")
			}
			referenceDocs = append(referenceDocs, doc)
		}
	})
}

func docsCommands(o *Options) *cobra.Command {
	root := &cobra.Command{Use: "docs", Short: "offline CRM fields and sourced WeCom parameter reference; never executes API calls"}
	root.AddCommand(&cobra.Command{Use: "coverage", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		loadReference()
		return o.print(referenceCoverage)
	}})
	root.AddCommand(&cobra.Command{Use: "search KEYWORD", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		loadReference()
		query := strings.ToLower(args[0])
		matches := []map[string]any{}
		for _, op := range catalog.Load().Operations {
			b, _ := json.Marshal(op)
			if strings.Contains(strings.ToLower(string(b)), query) {
				matches = append(matches, map[string]any{"kind": "crm_operation", "id": op.ID, "title": op.Summary, "execution": op.Execution, "executable_binding": op.Binding != nil})
			}
		}
		for _, doc := range referenceDocs {
			b, _ := json.Marshal([]any{doc.ID, doc.DocID, doc.Title, doc.Aliases, doc.Tables})
			if strings.Contains(strings.ToLower(string(b)), query) {
				matches = append(matches, map[string]any{"kind": "wecom_official", "id": doc.ID, "title": doc.Title, "status": doc.Status, "source_url": doc.SourceURL})
			}
		}
		return o.print(map[string]any{"query": args[0], "matches": matches, "count": len(matches), "network_requests": 0})
	}})
	root.AddCommand(&cobra.Command{Use: "official DOCUMENT_ID", Short: "exact official category ID, including aliases", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		loadReference()
		for _, doc := range referenceDocs {
			match := doc.ID == args[0]
			for _, alias := range doc.Aliases {
				match = match || alias["id"] == args[0]
			}
			if match {
				return o.print(map[string]any{"document": doc.Details, "crm_implementation": "not_implied_by_official_documentation", "examples": "reference_only; do not execute directly or reuse sample identities"})
			}
		}
		return fail("not_found", "unknown official document category ID; use docs search", 2)
	}})
	root.AddCommand(&cobra.Command{Use: "operation OPERATION_ID", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		op, ok := catalog.Find(args[0])
		if !ok {
			return fail("not_found", "unknown operation ID; use docs search", 2)
		}
		return o.print(operationDocumentation(op))
	}})
	return root
}

func operationDocumentation(op catalog.Operation) map[string]any {
	definitions := catalog.Load().Schemas
	fields := []map[string]any{}
	used := map[string]any{}
	var walk func(map[string]any, string, string, bool, map[string]bool)
	walk = func(schema map[string]any, location, path string, required bool, parents map[string]bool) {
		if ref, ok := schema["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			target, found := definitions[name].(map[string]any)
			if !found || parents[ref] {
				fields = append(fields, map[string]any{"location": location, "path": path, "reference": ref, "semantic_status": "consult_referenced_contract"})
				return
			}
			used[name] = target
			next := map[string]bool{}
			for k, v := range parents {
				next[k] = v
			}
			next[ref] = true
			walk(target, location, path, required, next)
			return
		}
		if path != "" {
			row := map[string]any{"location": location, "path": path, "required_at_parent": required, "schema": schema, "semantic_status": "description_absent_in_source_contract; do_not_guess"}
			if description, _ := schema["description"].(string); description != "" {
				row["description"], row["semantic_status"] = description, "source_contract_description"
			}
			fields = append(fields, row)
		}
		for _, composite := range []string{"allOf", "oneOf", "anyOf"} {
			items, _ := schema[composite].([]any)
			for _, item := range items {
				if s, ok := item.(map[string]any); ok {
					walk(s, location+"/"+composite, path, required, parents)
				}
			}
		}
		requiredNames := map[string]bool{}
		if names, ok := schema["required"].([]any); ok {
			for _, name := range names {
				if key, ok := name.(string); ok {
					requiredNames[key] = true
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if s, ok := properties[key].(map[string]any); ok {
				child := key
				if path != "" {
					child = path + "." + key
				}
				walk(s, location, child, requiredNames[key], parents)
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			walk(items, location, path+"[]", false, parents)
		}
	}
	if op.Binding != nil {
		declaredPaths := map[string]bool{}
		for _, param := range op.Binding.Parameters {
			schema, _ := param["schema"].(map[string]any)
			copySchema := map[string]any{}
			for k, v := range schema {
				copySchema[k] = v
			}
			if description, ok := param["description"].(string); ok {
				copySchema["description"] = description
			}
			location, _ := param["in"].(string)
			name, _ := param["name"].(string)
			if location == "path" {
				declaredPaths[name] = true
			}
			walk(copySchema, location, name, param["required"] == true, map[string]bool{})
		}
		for _, segment := range strings.Split(op.Binding.Path, "/") {
			if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
				name := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
				if !declaredPaths[name] {
					fields = append(fields, map[string]any{"location": "path", "path": name, "required_at_parent": true, "semantic_status": "path_placeholder_only; type_and_description_absent_in_catalog"})
				}
			}
		}
		walk(op.Binding.Body, "request_body", "", op.Binding.BodyRequired, map[string]bool{})
	}
	codes := make([]string, 0, len(op.Responses))
	for code := range op.Responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		response, _ := op.Responses[code].(map[string]any)
		content, _ := response["content"].(map[string]any)
		for media, raw := range content {
			v, _ := raw.(map[string]any)
			if schema, ok := v["schema"].(map[string]any); ok {
				walk(schema, "response/"+code+"/"+media, "", false, map[string]bool{})
			}
		}
	}
	return map[string]any{"operation": op, "fields": fields, "referenced_definitions": used, "source": "compiled CRM owner contract; official same-name fields are not automatically equivalent", "completeness": "only declared fields are shown; absent response schemas, unresolved response references and missing descriptions remain unknown", "example_workflow": []string{"aicrm-cli schema get " + op.ID, "aicrm-cli auth can-i " + op.ID + " --resource YOUR_RESOURCE_JSON", "aicrm-cli invoke " + op.ID + " [parameters from this contract] --dry-run", "execute explicitly, then read the original owner result"}, "business_permissions": "rechecked by server; documentation is not an authorization receipt"}
}
