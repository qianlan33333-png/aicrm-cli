package crmcli

import (
	"encoding/json"
	"fmt"
	"strings"

	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
	platformport "github.com/qianlan33333-png/aicrm-cli/internal/clientcontract"
)

func safeDiagnosticID(id string) string {
	if len(id) != 32 {
		return ""
	}
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return ""
		}
	}
	return id
}

func decodeErrorDetail(raw []byte, diagnosticID string) (platformport.ErrorDetail, bool) {
	var envelope map[string]json.RawMessage
	if StrictJSON(raw, &envelope) != nil {
		return platformport.ErrorDetail{}, false
	}
	value := envelope["error_detail"]
	if len(value) == 0 {
		var rpc struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(envelope["error"], &rpc) == nil {
			value = rpc.Data["error_detail"]
		}
	}
	var detail platformport.ErrorDetail
	if len(value) == 0 || json.Unmarshal(value, &detail) != nil || detail.Version != 1 {
		return detail, false
	}
	reason, known := catalog.FindErrorReason(detail.ReasonCode)
	if !known {
		return platformport.ErrorDetail{}, false
	}
	// Never trust server messages, recovery text, control characters or arbitrary
	// units. Only the compiled explanation and typed, bounded facts are shown.
	detail.Message, detail.NextAction, detail.DiagnosticID = reason.Message, reason.NextAction, diagnosticID
	if len(detail.Field) > 190 {
		detail.Field = ""
	}
	for _, c := range detail.Field {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.[]-", c)) {
			detail.Field = ""
			break
		}
	}
	if detail.Limit != nil && *detail.Limit < 0 || detail.Actual != nil && *detail.Actual < 0 {
		return platformport.ErrorDetail{}, false
	}
	switch detail.Unit {
	case "", "utf8_bytes", "bytes", "items", "seconds", "requests_per_window":
	default:
		detail.Unit = ""
	}
	if detail.RetryAfter != nil && (*detail.RetryAfter < 1 || detail.NextAction != "wait") {
		detail.RetryAfter = nil
	}
	if detail.ResourceID < 0 {
		detail.ResourceID = 0
	}
	if detail.WindowSeconds < 0 {
		detail.WindowSeconds = 0
	}
	switch detail.ExecutionState {
	case "", "not_submitted", "committed", "outcome_unknown":
	default:
		detail.ExecutionState = ""
	}
	if detail.Limit != nil && detail.Actual != nil && detail.Unit != "" {
		detail.Message += fmt.Sprintf("（实际 %d，上限 %d，单位 %s）", *detail.Actual, *detail.Limit, detail.Unit)
	}
	return detail, true
}
