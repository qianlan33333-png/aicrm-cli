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
			business := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer machine-test" || r.Header.Get("Cookie") != "" || r.Header.Get("X-CSRF-Token") != "" {
					t.Error("machine was changed to a human credential")
				}
				if r.URL.Path == "/open/v2/cli/context" {
					grant := "false"
					if tc.grant {
						grant = "true"
					}
					io.WriteString(w, `{"admin_cli_authorized":`+grant+`,"scopes":`+tc.scopes+`}`)
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
			if business != tc.want || (e == nil) != (tc.want == 1) {
				t.Fatalf("business=%d error=%v", business, e)
			}
		})
	}
}
