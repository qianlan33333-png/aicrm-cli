package port

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConstraintGuidanceCoversCompiledOperationsWithoutChangingBindings(t *testing.T) {
	c := Load()
	executable, blocked := 0, 0
	known := map[string]bool{}
	for _, r := range c.ErrorReasons {
		if r.Code == "" || r.Message == "" || r.Recovery == "" || known[r.Code] {
			t.Fatal("invalid error reason", r.Code)
		}
		known[r.Code] = true
	}
	for _, op := range c.Operations {
		if op.Binding == nil {
			blocked++
			if len(ConstraintsFor(op, c)) != 0 {
				t.Fatal("blocked operation has invented rules", op.ID)
			}
			continue
		}
		executable++
		before, _ := json.Marshal(op.Binding)
		g := Guidance(op)
		after, _ := json.Marshal(op.Binding)
		if string(before) != string(after) || g["runtime_boundary"] == "" || len(op.ErrorReasons) == 0 {
			t.Fatal("guidance mutated execution", op.ID)
		}
		for _, code := range op.ErrorReasons {
			if !known[code] {
				t.Fatal("unknown reason", code)
			}
		}
		for _, x := range ConstraintsFor(op, c) {
			if x.Source == "" || x.Reason == "" || x.Recovery == "" || x.Stage == "" || x.Unit == "" || x.Adjustable == "" || x.Evidence == "" {
				t.Fatal("incomplete explanation", op.ID, x.ID)
			}
		}
	}
	if executable == 0 || blocked == 0 || len(c.GlobalConstraints) < 10 {
		t.Fatal("missing inventory or local-command guidance")
	}
	op, _ := Find("admin.createAIAssistantPlan")
	raw, _ := json.Marshal(Guidance(op))
	if !strings.Contains(string(raw), "utf8_bytes") || !strings.Contains(string(raw), "64") {
		t.Fatal("material usage guidance absent")
	}
	t.Logf("catalog=%d executable=%d blocked=%d", len(c.Operations), executable, blocked)
}

func TestConstraintSchemaWalkHandlesNestedCyclesAndFalseWithoutGuessingUnits(t *testing.T) {
	c := Catalog{Schemas: map[string]any{"node": map[string]any{"properties": map[string]any{"next": map[string]any{"$ref": "#/components/schemas/node"}, "title": map[string]any{"maxLength": 64}}}}}
	op := Operation{ID: "fixture", Binding: &Binding{Body: map[string]any{"$ref": "#/components/schemas/node"}}}
	values := ConstraintsFor(op, c)
	if len(values) != 1 || values[0].Unit != "schema_length" || values[0].Field != "body.title" {
		t.Fatalf("%+v", values)
	}
}
