package crmcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

func isolateContextHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AICRM_CLI_CONFIG_DIR", filepath.Join(dir, "legacy-config"))
}
func executeContext(args ...string) (int, string) {
	var out, err bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &err)
	return code, out.String()
}
func TestCLICodexConfigAndIdentityIsolation(t *testing.T) {
	isolateContextHome(t)
	keyring.MockInit()
	legacy := &Options{}
	codex := &Options{Context: "codex"}
	if code, out := executeContext("profile", "add", "fixed", "https://crm.example"); code != 0 {
		t.Fatal(out)
	}
	legacyPath, _ := legacy.configPath()
	before, _ := os.ReadFile(legacyPath)
	if code, out := executeContext("--context", "codex", "profile", "list"); code != 0 || strings.Contains(out, "fixed") {
		t.Fatal("borrowed legacy config", out)
	}
	for _, args := range [][]string{{"profile", "add", "mine", "https://CRM.example/"}, {"profile", "add", "alias", "https://crm.example"}, {"profile", "use", "alias"}} {
		if code, out := executeContext(append([]string{"--context", "codex"}, args...)...); code != 0 {
			t.Fatal(out)
		}
	}
	after, _ := os.ReadFile(legacyPath)
	if !bytes.Equal(before, after) {
		t.Fatal("old config overwritten")
	}
	c, _ := codex.loadConfig()
	if c.Current != "alias" || c.Profiles["mine"].Origin != "https://crm.example" {
		t.Fatal(c)
	}
	origin := "https://crm.example"
	a := Credential{Origin: origin, Kind: "human", Token: "acl_old", CSRF: "csrf_old"}
	if err := StoreCredential(a); err != nil {
		t.Fatal(err)
	}
	if _, err := codex.loadCredential(origin); err == nil {
		t.Fatal("borrowed legacy keyring")
	}
	b := Credential{Origin: origin, Kind: "human", Token: "acl_codex", CSRF: "csrf_codex"}
	if err := codex.storeCredential(b); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "legacy-token.json")
	raw, _ := json.Marshal(a)
	os.WriteFile(file, raw, 0600)
	t.Setenv("AICRM_CLI_CREDENTIAL_FILE", file)
	t.Setenv("AICRM_CLI_TOKEN", "machine_env")
	t.Setenv("AICRM_CLI_CREDENTIAL_ORIGIN", origin)
	t.Setenv("AICRM_CLI_PASSWORD", "old_password")
	t.Setenv("AICRM_CLI_CLIENT_SECRET", "old_secret")
	got, err := codex.loadCredential(origin)
	if err != nil || got != b {
		t.Fatal("borrowed legacy env", err)
	}
	if _, err := codex.loadCredential("https://another.example"); err == nil {
		t.Fatal("cross origin credential")
	}
	got, err = LoadCredential(origin)
	if err != nil || got.Token != "machine_env" {
		t.Fatal("legacy precedence changed", err)
	}
	for _, name := range []string{"AICRM_CLI_PASSWORD", "AICRM_CLI_CLIENT_SECRET"} {
		codex.In = strings.NewReader("")
		if _, err := secretInput(codex, false, name); err == nil {
			t.Fatal("borrowed password/secret env")
		}
		codex.In = strings.NewReader("explicit\n")
		if got, err := secretInput(codex, true, name); err != nil || got != "explicit" {
			t.Fatal("explicit input lost", err)
		}
	}
	t.Setenv("AICRM_CLI_TOKEN", "")
	t.Setenv("AICRM_CLI_CREDENTIAL_FILE", "")
	if err := codex.deleteCredential(origin); err != nil {
		t.Fatal(err)
	}
	got, err = LoadCredential(origin)
	if err != nil || got != a {
		t.Fatal("Codex removal deleted legacy", err)
	}
	// Legacy deletion must also leave Codex credentials intact.
	codex.storeCredential(b)
	DeleteCredential(origin)
	got, err = codex.loadCredential(origin)
	if err != nil || got != b {
		t.Fatal("legacy removal deleted Codex", err)
	}
}

func TestCLICodexLoginLogoutAndMutationFence(t *testing.T) {
	isolateContextHome(t)
	keyring.MockInit()
	t.Setenv("AICRM_CLI_TOKEN", "legacy_environment")
	t.Setenv("AICRM_CLI_PASSWORD", "legacy_password")
	t.Setenv("AICRM_CLI_CLIENT_SECRET", "legacy_machine_secret")
	legacyFile := filepath.Join(t.TempDir(), "legacy.json")
	os.WriteFile(legacyFile, []byte("keep"), 0600)
	t.Setenv("AICRM_CLI_CREDENTIAL_FILE", legacyFile)
	logins, logouts, reads := 0, 0, 0
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/access/cli/login":
			logins++
			var v map[string]string
			json.NewDecoder(r.Body).Decode(&v)
			if v["password"] != "explicit" {
				t.Error("password inherited")
			}
			io.WriteString(w, `{"access_token":"acl_codex","csrf_token":"csrf_codex"}`)
		case "/oauth/token":
			logins++
			r.ParseForm()
			if r.Form.Get("client_secret") != "explicit" {
				t.Error("machine secret inherited")
			}
			io.WriteString(w, `{"access_token":"machine_codex"}`)
		case "/api/admin/access/cli/logout":
			logouts++
			if r.Header.Get("Authorization") != "Bearer acl_codex" {
				t.Error("wrong session revoked")
			}
			io.WriteString(w, `{}`)
		case "/api/admin/access/cli/session":
			reads++
			if r.Header.Get("Authorization") != "Bearer acl_codex" {
				t.Error("wrong session read")
			}
			io.WriteString(w, `{"kind":"human","actor_id":7}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	o := &Options{Context: "codex"}
	o.saveConfig(Config{Current: "mine", Profiles: map[string]Profile{"mine": {Origin: server.URL}}})
	prior := Credential{Origin: server.URL, Kind: "human", Token: "acl_legacy", CSRF: "csrf_legacy"}
	StoreCredential(prior)
	unlock, err := o.lockCredential(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"auth", "login", "--wecom", "--no-browser"}, {"auth", "login", "--username", "user", "--secret-stdin"}, {"auth", "logout"}} {
		code, out := executeContext(append([]string{"--context", "codex"}, args...)...)
		if code != 5 || !strings.Contains(out, "credential_busy") {
			t.Fatal("mutation was not fenced", code, out)
		}
	}
	if logins+logouts != 0 {
		t.Fatal("busy mutated server")
	}
	unlock()
	for _, kind := range []string{"human", "machine"} {
		var out, stderr bytes.Buffer
		args := []string{"--context", "codex", "auth", "login", "--secret-stdin"}
		if kind == "human" {
			args = append(args, "--username", "user")
		} else {
			args = append(args, "--client-id", "id")
		}
		if Run(args, strings.NewReader("explicit"), &out, &stderr) != 0 {
			t.Fatal(out.String())
		}
		got, err := o.loadCredential(server.URL)
		if err != nil || got.Kind != kind {
			t.Fatal("wrong saved kind", err)
		}
		if kind == "human" {
			if code, out := executeContext("--context", "codex", "auth", "whoami"); code != 0 || !strings.Contains(out, `"actor_id":7`) {
				t.Fatal(out)
			}
		}
		if code, out := executeContext("--context", "codex", "auth", "logout"); code != 0 {
			t.Fatal(out)
		}
		if _, err := o.loadCredential(server.URL); err == nil {
			t.Fatal("logout left credential")
		}
	}
	if logins != 2 || logouts != 1 || reads != 1 {
		t.Fatal("wrong server calls", logins, logouts, reads)
	}
	raw, _ := os.ReadFile(legacyFile)
	if string(raw) != "keep" {
		t.Fatal("legacy file deleted")
	}
	rawCred, err := keyring.Get("aicrm-cli", key(server.URL))
	var got Credential
	json.Unmarshal([]byte(rawCred), &got)
	if err != nil || got != prior {
		t.Fatal("legacy session changed", err)
	}
	if code, _ := executeContext("--context", "codex", "auth", "login", "--wecom", "--secret-output", filepath.Join(t.TempDir(), "unused")); code != 2 || logins != 2 {
		t.Fatal("Codex export accepted")
	}
	if code, _ := executeContext("--context", "typo", "profile", "list"); code != 2 {
		t.Fatal("unknown context silently fell back")
	}
}

func TestCLICodexCrossProcessLock(t *testing.T) {
	if path := os.Getenv("AICRM_TEST_CONTEXT_LOCK"); path != "" {
		unlock, err := lockCredentialFile(path)
		if os.Getenv("AICRM_TEST_CONTEXT_LOCK_EXPECT_BUSY") == "1" {
			var f *Failure
			if !errors.As(err, &f) || f.Code != "credential_busy" {
				t.Fatal("child acquired held lock", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("AICRM_TEST_CONTEXT_LOCK_HOLD") == "1" {
			fmt.Fprintln(os.Stdout, "locked")
			time.Sleep(time.Minute)
		}
		unlock()
		return
	}
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := lockCredentialFile(path)
	if err != nil {
		t.Fatal(err)
	}
	child := func(busy string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestCLICodexCrossProcessLock$")
		cmd.Env = append(os.Environ(), "AICRM_TEST_CONTEXT_LOCK="+path, "AICRM_TEST_CONTEXT_LOCK_EXPECT_BUSY="+busy)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %s %v", out, err)
		}
	}
	child("1")
	unlock()
	child("0")
	// The OS releases a crashed process's handle without PID files or leases.
	holder := exec.Command(os.Args[0], "-test.run=^TestCLICodexCrossProcessLock$")
	holder.Env = append(os.Environ(), "AICRM_TEST_CONTEXT_LOCK="+path, "AICRM_TEST_CONTEXT_LOCK_EXPECT_BUSY=0", "AICRM_TEST_CONTEXT_LOCK_HOLD=1")
	pipe, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Process.Kill() }()
	scanner := bufio.NewScanner(pipe)
	if !scanner.Scan() || scanner.Text() != "locked" {
		t.Fatal("holder failed to acquire lock")
	}
	if err = holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	child("0")
	o := &Options{Context: "codex"}
	isolateContextHome(t)
	a, err := o.lockCredential("https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	b, err := o.lockCredential("https://b.example")
	if err != nil {
		t.Fatal(err)
	}
	b()
	legacy := &Options{}
	free, err := legacy.lockCredential("https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	free()
}

func TestCLICodexWebhookExtensionRejectedBeforeRun(t *testing.T) {
	var out, stderr bytes.Buffer
	o := &Options{Out: &out, Err: &stderr, In: strings.NewReader("")}
	root := NewCommand(o)
	var group *cobra.Command
	for _, cmd := range root.Commands() {
		if cmd.Name() == "group-ops" {
			group = cmd
		}
	}
	if group == nil {
		t.Fatal("missing group operations")
	}
	called := false
	webhook := &cobra.Command{Use: "webhook"}
	webhook.AddCommand(&cobra.Command{Use: "send", RunE: func(*cobra.Command, []string) error { called = true; return nil }})
	group.AddCommand(webhook)
	root.SetArgs([]string{"--context", "codex", "group-ops", "webhook", "send"})
	var f *Failure
	err := root.Execute()
	if !errors.As(err, &f) || f.Code != "unavailable" || called {
		t.Fatal("extension reached credential resolver", err)
	}
}

func TestCLICodexUnavailableKeyringFailsBeforeGrant(t *testing.T) {
	keyring.MockInitWithError(errors.New("locked"))
	defer keyring.MockInit()
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	c, _ := NewClient(server.URL, Credential{}, time.Second)
	c.HTTP.Transport = server.Client().Transport
	var out, stderr bytes.Buffer
	o := &Options{Context: "codex", Format: "json", Out: &out, Err: &stderr}
	if err := o.wecomLogin(context.Background(), c, true, ""); err == nil || requests != 0 {
		t.Fatal("grant created without keyring", err)
	}
}

// The link is observable while the same login process is still pending.
type linkReadyWriter struct {
	bytes.Buffer
	once  sync.Once
	ready chan time.Time
}

func (w *linkReadyWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if strings.Contains(string(p), "终端核对码") {
		w.once.Do(func() { w.ready <- time.Now() })
	}
	return n, err
}
func TestCLICodexLinkBeforeAuthorizationAndInterruptCleanup(t *testing.T) {
	isolateContextHome(t)
	keyring.MockInit()
	var server *httptest.Server
	closed := make(chan struct{}, 1)
	started := make(chan struct{}, 1)
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/access/cli/device-authorization":
			started <- struct{}{}
			json.NewEncoder(w).Encode(map[string]any{"device_code": "acd_private", "user_code": "ABCD-EFGH", "verification_uri": server.URL + "/cli/authorize?code=ABCDEFGH", "expires_at": time.Now().Add(time.Minute), "interval": 1})
		case "/api/admin/access/cli/device-token":
			io.WriteString(w, `{"state":"pending"}`)
		case "/api/admin/access/cli/device-cancel":
			closed <- struct{}{}
			io.WriteString(w, `{"state":"closed"}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	storage := &Options{Context: "codex"}
	storage.saveConfig(Config{Current: "mine", Profiles: map[string]Profile{"mine": {Origin: server.URL}}})
	prior := Credential{Origin: server.URL, Kind: "human", Token: "acl_prior", CSRF: "csrf_prior"}
	storage.storeCredential(prior)
	legacy := prior
	legacy.Token = "acl_legacy"
	StoreCredential(legacy)
	var out bytes.Buffer
	stderr := &linkReadyWriter{ready: make(chan time.Time, 1)}
	cmd := NewCommand(&Options{Out: &out, Err: stderr, In: strings.NewReader("")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--context", "codex", "auth", "login", "--wecom", "--no-browser"})
	began := time.Now()
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case at := <-stderr.ready:
		t.Logf("fixture link visible in %s", at.Sub(began))
	case <-time.After(3 * time.Second):
		t.Fatal("link was blocked behind authorization")
	}
	select {
	case err := <-done:
		t.Fatal("pending process exited", err)
	default:
	}
	if code, text := executeContext("--context", "codex", "auth", "logout"); code != 5 {
		t.Fatal("pending login did not fence logout", text)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted login reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interruption failed to finish")
	}
	select {
	case <-closed:
	default:
		t.Fatal("interrupted grant not closed")
	}
	got, err := storage.loadCredential(server.URL)
	if err != nil || got != prior {
		t.Fatal("prior Codex identity lost", err)
	}
	got, err = LoadCredential(server.URL)
	if err != nil || got != legacy {
		t.Fatal("legacy identity changed", err)
	}
	unlock, err := storage.lockCredential(server.URL)
	if err != nil {
		t.Fatal("interruption retained lock", err)
	}
	unlock()
	if strings.Contains(stderr.String()+out.String(), "acd_private") {
		t.Fatal("private claim proof printed")
	}
}
