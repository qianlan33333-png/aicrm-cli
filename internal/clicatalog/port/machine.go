package port

import "strings"

// ManagedMachineScope is the shared transport rule used by discovery, the CLI
// client and the server adapter. It is metadata, never an authorization grant.
// Owner handlers still authorize the exact current subject and resource.
func ManagedMachineScope(op Operation) (string, bool) {
	if op.BlockedReason != "" || op.Binding == nil || op.Binding.Auth != "human" || !strings.HasPrefix(op.Binding.Path, "/api/") {
		return "", false
	}
	if op.Binding.Method == "GET" || op.Binding.Method == "HEAD" {
		return "read", true
	}
	return "write", true
}

// DelegatedOperation describes an existing Owner route callable with an
// explicitly delegated machine credential. It does not expose a new MCP tool.
type DelegatedOperation struct {
	OperationID         string `json:"operation_id"`
	Command             string `json:"command"`
	Method              string `json:"rest_method"`
	Path                string `json:"rest_path"`
	RequiredScope       string `json:"required_scope"`
	Capability          string `json:"capability"`
	IdempotencyRequired bool   `json:"idempotency_required"`
}

// DelegatedOperations includes only actions whose transport scope is granted.
// An empty slice is intentional when the independent delegation is absent.
func DelegatedOperations(authorized bool, scopes []string) []DelegatedOperation {
	items := []DelegatedOperation{}
	if !authorized {
		return items
	}
	granted := map[string]bool{}
	for _, scope := range scopes {
		granted[scope] = true
	}
	for _, op := range Load().Operations {
		scope, ok := ManagedMachineScope(op)
		if !ok || !granted[scope] {
			continue
		}
		items = append(items, DelegatedOperation{OperationID: op.ID, Command: "aicrm-cli " + op.Group + " " + op.Command, Method: op.Binding.Method, Path: op.Binding.Path, RequiredScope: scope, Capability: "cli.admin.execute", IdempotencyRequired: op.Binding.IdempotencyRequired})
	}
	return items
}
