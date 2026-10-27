package clientcontract

import "errors"

// ErrorDetail is optional transport metadata. It never authorizes a retry or
// changes an Owner's business decision. Messages must be static, safe text.
type ErrorDetail struct {
	Version        int    `json:"version"`
	ReasonCode     string `json:"reason_code"`
	Message        string `json:"message"`
	Field          string `json:"field,omitempty"`
	Limit          *int64 `json:"limit,omitempty"`
	Actual         *int64 `json:"actual,omitempty"`
	Unit           string `json:"unit,omitempty"`
	NextAction     string `json:"next_action"`
	RetryAfter     *int64 `json:"retry_after_seconds,omitempty"`
	WindowSeconds  int64  `json:"window_seconds,omitempty"`
	DiagnosticID   string `json:"diagnostic_id,omitempty"`
	ExecutionState string `json:"execution_state,omitempty"`
	ResourceID     int64  `json:"resource_id,omitempty"`
}

// ReasonError preserves errors.Is while attaching a reviewed explanation. Its
// Error method deliberately never includes the wrapped infrastructure error.
type ReasonError struct {
	Cause  error
	Detail ErrorDetail
}

func (e *ReasonError) Error() string            { return e.Detail.ReasonCode }
func (e *ReasonError) Unwrap() error            { return e.Cause }
func (e *ReasonError) ErrorDetail() ErrorDetail { return e.Detail }

func Explain(cause error, detail ErrorDetail) error {
	if cause == nil {
		return nil
	}
	detail.Version = 1
	return &ReasonError{Cause: cause, Detail: detail}
}

func DetailFromError(err error) (ErrorDetail, bool) {
	var explained interface{ ErrorDetail() ErrorDetail }
	if errors.As(err, &explained) {
		return explained.ErrorDetail(), true
	}
	return ErrorDetail{}, false
}

func PrefixErrorField(err error, prefix string) error {
	d, ok := DetailFromError(err)
	if !ok {
		return err
	}
	if d.Field == "" {
		d.Field = prefix
	} else {
		d.Field = prefix + "." + d.Field
	}
	return Explain(err, d)
}
