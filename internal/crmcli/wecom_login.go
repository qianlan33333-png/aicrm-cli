package crmcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zalando/go-keyring"
)

func openLoginBrowser(ctx context.Context, address string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "windows":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		command = "xdg-open"
	}
	args = append(args, address)
	return exec.CommandContext(ctx, command, args...).Run()
}
func cliDeviceJSON(ctx context.Context, c *Client, path string, body any, result any) error {
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	r, e := c.request(ctx, "POST", path, "application/json", bytes.NewReader(b), "")
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if e = json.NewDecoder(r.Body).Decode(result); e != nil {
		return fail("protocol", "CLI authorization response unavailable", 1)
	}
	return nil
}
func (o *Options) wecomLogin(ctx context.Context, c *Client, noBrowser bool, outputFile string) (resultErr error) {
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	// Preserve the prior credential without consulting environment-token overrides.
	var previous string
	hadPrevious := false
	if outputFile != "" {
		if _, e := os.Lstat(outputFile); !errors.Is(e, os.ErrNotExist) {
			return fail("credential_storage", "credential output must be a new private file", 1)
		}
	} else {
		var e error
		previous, e = keyring.Get("aicrm-cli", key(c.Origin))
		hadPrevious = e == nil
		if e != nil && !errors.Is(e, keyring.ErrNotFound) {
			return fail("credential_storage", "system keyring unavailable; use an explicit --secret-output on a headless host", 1)
		}
	}
	var start struct {
		DeviceCode string    `json:"device_code"`
		UserCode   string    `json:"user_code"`
		URI        string    `json:"verification_uri"`
		Expires    time.Time `json:"expires_at"`
		Interval   int       `json:"interval"`
	}
	name, e := os.Hostname()
	if e != nil || name == "" {
		name = "CLI terminal"
	}
	if e = cliDeviceJSON(ctx, c, "/api/admin/access/cli/device-authorization", map[string]string{"device_name": name}, &start); e != nil {
		return e
	}
	if !strings.HasPrefix(start.DeviceCode, "acd_") || start.UserCode == "" {
		return fail("protocol", "invalid CLI authorization request", 1)
	}
	stored := false
	complete := false
	defer func() {
		if complete {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), c.HTTP.Timeout)
		defer cancel()
		var closed struct {
			State string `json:"state"`
		}
		closeErr := cliDeviceJSON(closeCtx, c, "/api/admin/access/cli/device-cancel", map[string]string{"device_code": start.DeviceCode}, &closed)
		if closeErr != nil || closed.State != "closed" {
			resultErr = fail("authorization_outcome_unknown", "login failed; revocation could not be confirmed; no automatic login retry was started", 1)
		}
		if stored {
			var restoreErr error
			if outputFile != "" {
				restoreErr = os.Remove(outputFile)
			} else if hadPrevious {
				restoreErr = keyring.Set("aicrm-cli", key(c.Origin), previous)
			} else {
				restoreErr = DeleteCredential(c.Origin)
				if errors.Is(restoreErr, keyring.ErrNotFound) {
					restoreErr = nil
				}
			}
			if restoreErr != nil {
				if closeErr != nil || closed.State != "closed" {
					resultErr = fail("authorization_outcome_unknown", "revocation and restoring the previous local credential could not be confirmed; no automatic retry was started", 1)
				} else {
					resultErr = fail("credential_storage", "login failed; restoring the previous local credential could not be confirmed", 1)
				}
			}
		}
	}()
	u, e := url.Parse(start.URI)
	if e != nil || u.Scheme+"://"+u.Host != c.Origin || u.Path != "/cli/authorize" || u.User != nil || u.Fragment != "" || start.Interval < 1 || !start.Expires.After(time.Now()) || start.Expires.After(time.Now().Add(15*time.Minute)) {
		return fail("protocol", "invalid CLI authorization link or deadline", 1)
	}
	fmt.Fprintf(o.Err, "在浏览器中打开：%s\n终端核对码：%s\n只确认你自己发起的 CLI 登录。\n", start.URI, start.UserCode)
	if !noBrowser {
		open := o.OpenBrowser
		if open == nil {
			open = openLoginBrowser
		}
		if e = open(ctx, start.URI); e != nil {
			fmt.Fprintln(o.Err, "浏览器未能自动打开，请复制上面的链接到浏览器。")
		}
	}
	waitCtx, cancel := context.WithDeadline(ctx, start.Expires)
	defer cancel()
	var issued struct {
		State   string     `json:"state"`
		Token   string     `json:"access_token"`
		CSRF    string     `json:"csrf_token"`
		Expires *time.Time `json:"expires_at"`
	}
	for {
		issued.Token = ""
		issued.CSRF = ""
		e = cliDeviceJSON(waitCtx, c, "/api/admin/access/cli/device-token", map[string]string{"device_code": start.DeviceCode}, &issued)
		var f *Failure
		if e != nil && !(errors.As(e, &f) && f.Status == http.StatusTooManyRequests) {
			return e
		}
		if e == nil {
			switch issued.State {
			case "issued":
				if !strings.HasPrefix(issued.Token, "acl_") || issued.CSRF == "" {
					return fail("protocol", "authorization did not issue a CLI credential", 1)
				}
				goto save
			case "pending":
			case "consumed":
				return fail("authorization_outcome_unknown", "credential was already issued; closing this login request before another attempt", 1)
			case "closed", "expired":
				return fail("authentication", "CLI authorization closed or expired", 3)
			default:
				return fail("protocol", "invalid CLI authorization state", 1)
			}
		}
		timer := time.NewTimer(time.Duration(start.Interval) * time.Second)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return fail("authentication", "CLI authorization interrupted or expired", 3)
		case <-timer.C:
		}
	}
save:
	credential := Credential{Origin: c.Origin, Kind: "human", Token: issued.Token, CSRF: issued.CSRF}
	if outputFile != "" {
		b, _ := json.Marshal(credential)
		e = privateFile(outputFile, b, false)
	} else {
		stored = true
		e = StoreCredential(credential)
	}
	if e != nil {
		return fail("credential_storage", "new credential could not be saved; this login will be revoked", 1)
	}
	stored = true
	// Read through the saved representation before verifying the server identity.
	var saved Credential
	if outputFile != "" {
		b, err := os.ReadFile(outputFile)
		if err != nil {
			return fail("credential_storage", "saved CLI credential unavailable", 1)
		}
		if json.Unmarshal(b, &saved) != nil {
			return fail("credential_storage", "saved CLI credential invalid", 1)
		}
	} else {
		b, err := keyring.Get("aicrm-cli", key(c.Origin))
		if err != nil || json.Unmarshal([]byte(b), &saved) != nil {
			return fail("credential_storage", "saved CLI credential unavailable", 1)
		}
	}
	if saved != credential {
		return fail("credential_storage", "saved CLI credential differs from issued credential", 1)
	}
	authenticated := *c
	authenticated.Credential = saved
	r, e := authenticated.request(ctx, "GET", "/api/admin/access/cli/session", "", nil, "")
	if e != nil {
		return e
	}
	defer r.Body.Close()
	var identity struct {
		Kind    string `json:"kind"`
		ActorID int64  `json:"actor_id"`
		Origin  string `json:"server_origin"`
	}
	if json.NewDecoder(r.Body).Decode(&identity) != nil || identity.Kind != "human" || identity.ActorID <= 0 || identity.Origin != c.Origin {
		return fail("protocol", "CLI identity readback did not match this origin", 1)
	}
	complete = true
	storage := "system_keyring"
	if outputFile != "" {
		storage = "private_file"
	}
	return o.print(map[string]any{"authenticated": true, "kind": "human", "actor_id": identity.ActorID, "origin": c.Origin, "credential_storage": storage, "expires_at": issued.Expires, "automatic_renewal": false})
}
