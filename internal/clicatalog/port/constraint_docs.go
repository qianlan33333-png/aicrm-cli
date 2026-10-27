package port

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ConstraintsFor explains the existing schema. It is intentionally never used
// to authorize requests or to invent Provider limits from same-named fields.
func ConstraintsFor(op Operation, c Catalog) []Constraint {
	if op.Binding == nil {
		return nil
	}
	out := append([]Constraint(nil), op.Constraints...)
	if op.Binding.Auth != "" {
		out = append(out, Constraint{ID: "operation_authorization", Value: map[string]any{"identity": op.Binding.Auth, "required_scope": op.Binding.EffectiveScope(), "capability": op.Binding.Capability, "csrf_required": op.Binding.CSRFRequired}, Unit: "authorization", Stage: "authorization", Reason: "Owner 要求对应身份、操作及资源授权，防止跨主体读取或写入。机器调用管理接口还需独立 CLI 委托；目录和 can-i 不能替代执行时授权。", Recovery: "同一上下文读取 whoami/doctor，核对当前主体、scope、capability 和 CLI 委托；由有权管理员补充对应授权。", Adjustable: "通过既有授权配置显式调整；不能由请求体自报。", Source: op.Evidence, Evidence: "compiled_binding; owner_rechecks_at_execution"})
	}
	if op.Binding.IdempotencyRequired {
		out = append(out, Constraint{ID: "operation_idempotency", Field: "header.Idempotency-Key", Value: true, Unit: "presence", Stage: "acceptance", Reason: "该操作以原键识别同一次业务接受，防止重复计划或外部效果。", Recovery: "逻辑同一请求保持原键与业务内容；冲突或结果未知先读取原对象，不能换键重发。", Adjustable: "原有业务合同，不由调用方关闭。", Source: op.Evidence, Evidence: "compiled_binding"})
	}
	var walk func(map[string]any, string, map[string]bool)
	walk = func(schema map[string]any, field string, refs map[string]bool) {
		if ref, ok := schema["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
			if refs[ref] {
				return
			}
			next := make(map[string]bool, len(refs)+1)
			for k, v := range refs {
				next[k] = v
			}
			next[ref] = true
			if target, ok := c.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any); ok {
				walk(target, field, next)
			}
			return
		}
		for _, key := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "minLength", "maxLength", "minItems", "maxItems", "pattern", "enum", "additionalProperties"} {
			value, ok := schema[key]
			if !ok {
				continue
			}
			if key == "additionalProperties" && value != false {
				continue
			}
			unit, why, recovery := "number", "Owner 合同规定的业务值域，防止提交合同之外的数据。", "按对应字段声明修改数值。"
			switch key {
			case "minLength", "maxLength":
				unit = "schema_length"
				why = "OpenAPI 声明的字段长度；不得把同名字段推断成企微限制。服务端字节/字符口径以对应 Owner 用途规则为准。"
				recovery = "按字段说明准备内容；发送用途同时检查明确的 UTF-8 字节限制。"
			case "minItems", "maxItems":
				unit = "items"
				why = "Owner 声明的集合范围，当前接口不接受范围之外的列表。阈值不等于已完成容量压测。"
				recovery = "使用现有分页或显式分批；不能换键重复提交结果未知的批次。"
			case "pattern", "enum":
				unit = "format"
				why = "Owner 只支持列出的格式或业务状态，拒绝无法解释的值。"
				recovery = "从现有资源或声明枚举取值，不猜测状态或标识。"
			case "additionalProperties":
				unit = "field_set"
				why = "Owner 拒绝未声明的字段，避免拼写错误和越界输入静默生效。"
				recovery = "删除未知字段并读取对应操作 schema。"
			}
			if description, ok := schema["description"].(string); ok && description != "" {
				why += " 字段说明：" + description
			}
			out = append(out, Constraint{ID: "schema:" + field + ":" + key, Field: field, Value: value, Unit: unit, Stage: "input_validation", Reason: why, Recovery: recovery, Adjustable: "接口合同；不能由调用者绕过。配置型字段以当前服务端配置为准。", Source: "api/openapi.yaml#" + op.ID + "/" + field + "/" + key, Evidence: "source_contract; capacity_or_encoding_claims_not_inferred"})
		}
		if required, ok := schema["required"].([]any); ok {
			for _, key := range required {
				if name, ok := key.(string); ok {
					out = append(out, Constraint{ID: "required:" + field + "." + name, Field: field + "." + name, Value: true, Unit: "presence", Stage: "input_validation", Reason: "Owner 需要此字段才能确定业务指令，省略时不能安全猜测。", Recovery: "按字段合同补齐，身份和资源 ID 从已授权读取结果取得。", Adjustable: "不可由调用方省略", Source: "api/openapi.yaml#" + op.ID + "/" + field, Evidence: "source_contract"})
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			keys := make([]string, 0, len(props))
			for key := range props {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if child, ok := props[key].(map[string]any); ok {
					walk(child, strings.TrimPrefix(field+"."+key, "."), refs)
				}
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			walk(items, field+"[]", refs)
		}
		for _, key := range []string{"allOf", "oneOf", "anyOf"} {
			if list, ok := schema[key].([]any); ok {
				for i, item := range list {
					if child, ok := item.(map[string]any); ok {
						walk(child, fmt.Sprintf("%s.%s[%d]", field, key, i), refs)
					}
				}
			}
		}
	}
	for _, p := range op.Binding.Parameters {
		schema, _ := p["schema"].(map[string]any)
		field := fmt.Sprint(p["in"]) + "." + fmt.Sprint(p["name"])
		walk(schema, field, map[string]bool{})
		if p["required"] == true {
			out = append(out, Constraint{ID: "required:" + field, Field: field, Value: true, Unit: "presence", Stage: "input_validation", Reason: "路径或请求参数用于确定本次操作对象，不能安全省略。", Recovery: "从已授权读取结果取得参数，使用 --param 或 --query 按声明传入。", Adjustable: "接口合同", Source: op.Evidence, Evidence: "compiled_parameter"})
		}
	}
	walk(op.Binding.Body, "body", map[string]bool{})
	return out
}

func Guidance(op Operation) map[string]any {
	c := Load()
	reasons := []ErrorReason{}
	for _, code := range op.ErrorReasons {
		if reason, ok := FindErrorReason(code); ok {
			reasons = append(reasons, reason)
		}
	}
	return map[string]any{"constraints": ConstraintsFor(op, c), "global_constraints": c.GlobalConstraints, "error_reasons": reasons, "runtime_boundary": "离线合同默认值与用途说明；运行配置以已授权的现有配置查询和本次错误 detail 实际值为准。说明不是执行授权。"}
}

func ConstraintValue(v any) string { raw, _ := json.Marshal(v); return string(raw) }
