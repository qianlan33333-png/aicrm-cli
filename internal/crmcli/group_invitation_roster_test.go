package crmcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestCLIGroupInvitationRosterFollowsAllGroupCursors(t *testing.T) {
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	t.Setenv("AICRM_CLI_NO_UPDATE_NOTIFIER", "1")

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/admin/group-invitations/15/members" ||
			r.Header.Get("Authorization") != "Bearer acl_ROSTER" {
			t.Error("expected an authenticated CLI read for the original plan")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			fmt.Fprint(w, `{"plan_id":15,"chat_id":"a","members":[{"name":"成员甲"}],"next_cursor":"15:3:1"}`)
		case "15:3:1":
			fmt.Fprint(w, `{"plan_id":15,"chat_id":"b","members":[{"name":"成员乙"}],"next_cursor":""}`)
		default:
			t.Error("CLI changed the returned cursor")
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	if err := SaveConfig(Config{Current: "test", Profiles: map[string]Profile{"test": {Origin: server.URL}}}); err != nil {
		t.Fatal(err)
	}
	if err := StoreCredential(Credential{Origin: server.URL, Kind: "human", Token: "acl_ROSTER"}); err != nil {
		t.Fatal(err)
	}

	var out, stderr bytes.Buffer
	args := []string{"group-invitations", "group-invitation-plan-members", "list", "--param", "id=15", "--all", "--format", "ndjson"}
	if code := Run(args, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatalf("CLI exit %d: %s %s", code, out.String(), stderr.String())
	}
	decoder := json.NewDecoder(&out)
	for _, expected := range []string{"a", "b"} {
		var page struct {
			Data struct {
				ChatID string `json:"chat_id"`
			} `json:"data"`
		}
		if err := decoder.Decode(&page); err != nil || page.Data.ChatID != expected {
			t.Fatalf("expected group %s, got %+v (%v)", expected, page, err)
		}
	}
	if decoder.More() {
		t.Fatal("unexpected extra group")
	}
}
