package crmcli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMachineAdminCLIUsesOnlyMachineBearerAndExplicitRemoteGrant(t *testing.T) {
	for _, tc := range []struct {
		name, scopes string
		grant        bool
		want         int
	}{{"full", `["read","write"]`, true, 1}, {"read_only", `["read"]`, true, 0}, {"ordinary_machine", `["read","write"]`, false, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			business, requests := 0, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "Bearer machine-test" || r.Header.Get("Cookie") != "" || r.Header.Get("X-CSRF-Token") != "" {
					t.Error("machine was changed to a human credential")
				}
				if r.URL.Path == "/open/v2/cli/context" {
					t.Error("client made a separate authorization context request")
					return
				}
				if !tc.grant || tc.scopes != `["read","write"]` {
					http.Error(w, `{"error":"permission_denied"}`, http.StatusForbidden)
					return
				}
				business++
				if r.Header.Get("X-AICRM-CLI-Operation") != "test" {
					t.Error("operation binding missing")
				}
				io.WriteString(w, `{}`)
			}))
			defer server.Close()
			c, _ := NewClient(server.URL, Credential{Kind: "machine", Token: "machine-test"}, time.Second)
			c.HTTP.Transport = server.Client().Transport
			resp, e := c.Call(context.Background(), testOperation("POST", "/api/admin/registered", "human"), CallInput{})
			if resp != nil {
				resp.Body.Close()
			}
			if business != tc.want || requests != 1 || (e == nil) != (tc.want == 1) {
				t.Fatalf("business=%d requests=%d error=%v", business, requests, e)
			}
		})
	}
}
