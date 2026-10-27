package port

import platformport "github.com/qianlan33333-png/aicrm-cli/internal/clientcontract"

// Constraint is documentation, not another validation engine. Runtime values
// remain owned by the original service/configuration/Provider contract.
type Constraint struct {
	ID         string `json:"id"`
	Field      string `json:"field,omitempty"`
	Value      any    `json:"value,omitempty"`
	Unit       string `json:"unit"`
	Stage      string `json:"stage"`
	Reason     string `json:"reason"`
	Recovery   string `json:"recovery"`
	Adjustable string `json:"adjustable"`
	Source     string `json:"source"`
	Evidence   string `json:"evidence"`
}

type ErrorReason = platformport.ErrorReason

func FindErrorReason(code string) (ErrorReason, bool) {
	return platformport.FindErrorReason(code)
}
