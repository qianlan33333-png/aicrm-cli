package clientcontract

import (
	_ "embed"
	"encoding/json"
)

// ErrorReason describes a transport result; it does not execute business rules.
type ErrorReason struct {
	Code       string `json:"reason_code"`
	Message    string `json:"message"`
	NextAction string `json:"next_action"`
	Recovery   string `json:"recovery"`
}

//go:embed error_reasons.json
var errorReasonsJSON []byte

var errorReasons = func() []ErrorReason {
	var out []ErrorReason
	if err := json.Unmarshal(errorReasonsJSON, &out); err != nil {
		panic("invalid error explanation contract")
	}
	return out
}()

func ErrorReasons() []ErrorReason { return append([]ErrorReason(nil), errorReasons...) }
func FindErrorReason(code string) (ErrorReason, bool) {
	for _, reason := range errorReasons {
		if reason.Code == code {
			return reason, true
		}
	}
	return ErrorReason{}, false
}

func DescribeError(code string) ErrorDetail {
	r, ok := FindErrorReason(code)
	if !ok {
		r, _ = FindErrorReason("service_unavailable")
	}
	return ErrorDetail{Version: 1, ReasonCode: r.Code, Message: r.Message, NextAction: r.NextAction}
}
