package crmcli

import (
	"context"
	"encoding/json"
	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
)

// The independently signed webhook extension preserves its existing explicit
// machine delegation check before it freezes a new plan snapshot. The shared
// V4 client's ordinary server-owned operations do not own this extension gate.
func (c *Client) authorizePlanPushReader(ctx context.Context) error {
	if c.Credential.Kind == "human" {
		return nil
	}
	if c.Credential.Kind != "machine" {
		return fail("permission", "plan snapshot requires an authorized reader", 4)
	}
	op, ok := catalog.Find("admin.getGroupOpsPlan")
	if !ok {
		return fail("unavailable", "plan read operation is unavailable", 4)
	}
	scope, ok := catalog.ManagedMachineScope(op)
	if !ok {
		return fail("permission", "plan read has no machine delegation binding", 4)
	}
	response, e := c.request(ctx, "GET", "/open/v2/cli/context", "", nil, "")
	if e != nil {
		return e
	}
	defer response.Body.Close()
	var current struct {
		Authorized bool     `json:"admin_cli_authorized"`
		Scopes     []string `json:"scopes"`
	}
	if json.NewDecoder(response.Body).Decode(&current) != nil {
		return fail("protocol", "machine authorization context unavailable", 1)
	}
	if current.Authorized {
		for _, granted := range current.Scopes {
			if granted == scope {
				return nil
			}
		}
	}
	return fail("permission", "machine CLI delegation or plan read scope is absent; no snapshot or push created", 4)
}
