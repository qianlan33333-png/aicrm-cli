package crmcli

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
	clientconfig "github.com/qianlan33333-png/aicrm-cli/internal/clientconfig"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

// These limits and headers belong to the installed V4 inbound protocol.
const webhookKeyringService = "aicrm-cli-groupops-webhook"
const webhookBodyLimit = 64 * 1024
const webhookClientID = "aicrm-webhook-group-ops"

var webhookOpaque = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type webhookCredential struct {
	Origin string `json:"origin"`
	Secret string `json:"secret"`
}

func validWebhookSecret(secret string) bool {
	return len(secret) >= 32 && len(secret) <= 4096 && strings.TrimSpace(secret) == secret
}

func loadWebhookSecret(origin string) (string, string, error) {
	env := clientconfig.LoadGroupOpsWebhookEnvironment()
	if env.Secret != "" {
		if env.Origin != origin || !validWebhookSecret(env.Secret) {
			return "", "", fail("authentication", "webhook environment requires a valid secret and matching AICRM_CLI_GROUP_OPS_WEBHOOK_ORIGIN", 3)
		}
		return env.Secret, "environment", nil
	}
	raw, err := keyring.Get(webhookKeyringService, key(origin))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", "system_keyring", nil
	}
	if err != nil {
		return "", "", fail("authentication", "webhook system keyring is unavailable; no plaintext fallback", 3)
	}
	var value webhookCredential
	if json.Unmarshal([]byte(raw), &value) != nil || value.Origin != origin || !validWebhookSecret(value.Secret) {
		return "", "", fail("authentication", "stored webhook credential is invalid or belongs to another origin", 3)
	}
	return value.Secret, "system_keyring", nil
}

func webhookSecretInput(o *Options, stdin bool) (string, error) {
	if stdin {
		body, err := io.ReadAll(io.LimitReader(o.In, 4099))
		if err != nil {
			return "", fail("io", "could not read webhook secret", 1)
		}
		return strings.TrimRight(string(body), "\r\n"), nil
	}
	f, ok := o.In.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", fail("authentication", "webhook secret requires hidden terminal input or --secret-stdin", 3)
	}
	fmt.Fprint(o.Err, "Webhook signing secret: ")
	body, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(o.Err)
	if err != nil {
		return "", fail("io", "could not read webhook secret", 1)
	}
	return string(body), nil
}

func groupOpsWebhookCommands(o *Options) *cobra.Command {
	root := &cobra.Command{Use: "webhook", Short: "signed dynamic Group Ops requests; employee confirmation remains required"}
	var stdin bool
	configure := &cobra.Command{Use: "configure", Args: cobra.NoArgs, Short: "store this profile's signing secret in the independent system-keyring slot", RunE: func(*cobra.Command, []string) error {
		origin, err := o.origin()
		if err != nil {
			return err
		}
		secret, err := webhookSecretInput(o, stdin)
		if err != nil {
			return err
		}
		if !validWebhookSecret(secret) {
			return fail("validation", "webhook secret requires 32-4096 bytes with no surrounding whitespace", 2)
		}
		body, _ := json.Marshal(webhookCredential{Origin: origin, Secret: secret})
		if keyring.Set(webhookKeyringService, key(origin), string(body)) != nil {
			return fail("authentication", "webhook secret could not be stored in the system keyring; no plaintext fallback", 3)
		}
		return o.print(map[string]any{"origin": origin, "configured": true, "credential_storage": "system_keyring", "human_or_machine_login_changed": false})
	}}
	configure.Flags().BoolVar(&stdin, "secret-stdin", false, "read the signing secret from protected stdin")
	root.AddCommand(configure)
	root.AddCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, Short: "check local signing configuration without contacting the server", RunE: func(*cobra.Command, []string) error {
		origin, err := o.origin()
		if err != nil {
			return err
		}
		secret, source, err := loadWebhookSecret(origin)
		if err != nil {
			return err
		}
		return o.print(map[string]any{"origin": origin, "configured": secret != "", "credential_source": source, "server_or_provider_verified": false})
	}})
	root.AddCommand(&cobra.Command{Use: "forget", Args: cobra.NoArgs, Short: "remove only this origin's stored webhook secret", RunE: func(*cobra.Command, []string) error {
		origin, err := o.origin()
		if err != nil {
			return err
		}
		if err = keyring.Delete(webhookKeyringService, key(origin)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fail("authentication", "webhook credential could not be removed from the system keyring", 3)
		}
		return o.print(map[string]any{"origin": origin, "local_webhook_credential_removed": true, "environment_override_present": clientconfig.LoadGroupOpsWebhookEnvironment().Secret != "", "human_or_machine_login_changed": false, "server_secret_rotated": false})
	}})
	var input, event string
	var dryRun bool
	send := &cobra.Command{Use: "send", Args: cobra.NoArgs, Short: "sign and submit one dynamic request; never automatically retry a write", RunE: func(cmd *cobra.Command, _ []string) error {
		return o.sendGroupOpsWebhook(cmd, input, event, dryRun)
	}}
	send.Flags().StringVar(&input, "input", "", "complete signed request JSON file, or - for stdin")
	send.Flags().StringVar(&event, "event-id", "", "stable 16-256 character event ID; retain it with the identical input for recovery")
	send.Flags().BoolVar(&dryRun, "dry-run", false, "validate locally without reading a secret or making a network request")
	root.AddCommand(send, groupOpsWebhookPushCommand(o))
	return root
}

func readWebhookBody(o *Options, path string) ([]byte, error) {
	if path == "" {
		return nil, fail("validation", "webhook send requires --input FILE or --input -", 2)
	}
	reader := o.In
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fail("io", "could not open webhook input", 1)
		}
		defer file.Close()
		reader = file
	}
	body, err := io.ReadAll(io.LimitReader(reader, webhookBodyLimit+1))
	if err != nil {
		return nil, fail("io", "could not read webhook input", 1)
	}
	if len(body) > webhookBodyLimit || !utf8.Valid(body) {
		return nil, fail("validation", "webhook JSON must be UTF-8 and at most 64 KiB", 2)
	}
	return body, nil
}

func validWebhookEvent(event string) bool {
	if len(event) < 16 || len(event) > 256 {
		return false
	}
	for _, character := range event {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validWebhookText(text string, limit int) bool {
	return text != "" && strings.TrimSpace(text) == text && utf8.RuneCountInString(text) <= limit && !strings.ContainsRune(text, '\x00')
}

func validateWebhookBody(body []byte) (string, int, int, error) {
	var value any
	if err := StrictJSON(body, &value); err != nil {
		return "", 0, 0, err
	}
	definitions := catalog.Load().Schemas
	if err := Validate(value, map[string]any{"$ref": "#/components/schemas/GroupOpsWebhookInboundRequest"}, definitions); err != nil {
		return "", 0, 0, err
	}
	m, ok := value.(map[string]any)
	if !ok {
		return "", 0, 0, fail("validation", "webhook request must be an object", 2)
	}
	reference, _ := m["webhook_reference"].(string)
	if !webhookOpaque.MatchString(reference) || reference == "." || reference == ".." {
		return "", 0, 0, fail("validation", "invalid webhook reference", 2)
	}
	targets, _ := m["target_chat_references"].([]any)
	messages, _ := m["messages"].([]any)
	if len(targets) == 0 || len(messages) == 0 || len(messages) > 10 {
		return "", 0, 0, fail("validation", "webhook requires bound targets and 1-10 messages", 2)
	}
	for _, item := range targets {
		target, _ := item.(string)
		if !webhookOpaque.MatchString(target) {
			return "", 0, 0, fail("validation", "target must be a plan-bound opaque group reference", 2)
		}
	}
	attachments, miniPrograms := 0, 0
	for index, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			return "", 0, 0, fail("validation", "message must be an object", 2)
		}
		kind, _ := message["type"].(string)
		schema, idField := "", ""
		switch kind {
		case "text":
			schema = "GroupOpsWebhookTextMessage"
			text, _ := message["text"].(string)
			if index != 0 || !validWebhookText(text, 1000) {
				return "", 0, 0, fail("validation", "text must be first, nonblank, without surrounding whitespace or NUL, and at most 1000 characters", 2)
			}
		case "image":
			schema, idField = "GroupOpsWebhookImageMessage", "image_id"
			attachments++
		case "file":
			schema, idField = "GroupOpsWebhookFileMessage", "attachment_id"
			attachments++
		case "miniprogram":
			attachments++
			miniPrograms++
			if _, stored := message["miniprogram_id"]; stored {
				schema, idField = "GroupOpsWebhookStoredMiniProgramMessage", "miniprogram_id"
			} else {
				schema = "GroupOpsWebhookDailyLessonMiniProgramMessage"
				appID, _ := message["appid"].(string)
				path, _ := message["path"].(string)
				title, _ := message["title"].(string)
				if !validWebhookText(appID, 128) || !validWebhookText(path, 1024) || !validWebhookText(title, 64) || len(title) > 64 || strings.ContainsAny(title, "\n\r\t") {
					return "", 0, 0, fail("validation", "miniprogram requires appid/path and a title of at most 64 UTF-8 bytes", 2)
				}
				for _, character := range title {
					if character < 0x20 || character == 0x7f {
						return "", 0, 0, fail("validation", "miniprogram title cannot contain control characters", 2)
					}
				}
			}
		default:
			return "", 0, 0, fail("validation", "supported messages are text, image, file and miniprogram", 2)
		}
		if err := Validate(message, map[string]any{"$ref": "#/components/schemas/" + schema}, definitions); err != nil {
			return "", 0, 0, err
		}
		if idField != "" {
			number, ok := message[idField].(json.Number)
			id, err := number.Int64()
			if !ok || err != nil || id < 1 {
				return "", 0, 0, fail("validation", "material ID must be a positive JSON integer", 2)
			}
		}
	}
	if attachments > 9 || miniPrograms > 1 {
		return "", 0, 0, fail("validation", "webhook supports at most nine attachments and one miniprogram", 2)
	}
	return reference, len(targets), len(messages), nil
}

func webhookDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (o *Options) sendGroupOpsWebhook(cmd *cobra.Command, input, event string, dryRun bool) error {
	if !validWebhookEvent(event) {
		return fail("validation", "--event-id requires 16-256 visible ASCII characters without spaces", 2)
	}
	origin, err := o.origin()
	if err != nil {
		return err
	}
	body, err := readWebhookBody(o, input)
	if err != nil {
		return err
	}
	_, targets, messages, err := validateWebhookBody(body)
	if err != nil {
		return err
	}
	result := map[string]any{"operation_id": "client.groupops.webhook.send", "origin": origin, "event_digest": webhookDigest([]byte(event)), "request_digest": webhookDigest(body), "target_count": targets, "message_count": messages, "delivery_proven": false}
	if dryRun {
		result["dry_run"] = true
		result["network_request_sent"] = false
		result["authorization_and_bound_targets_verified"] = false
		return o.print(result)
	}
	result, err = o.submitGroupOpsWebhook(cmd, origin, body, event)
	if err != nil {
		return err
	}
	return o.print(result)
}

// Both complete-JSON send and plan push share this exact single-write protocol.
func (o *Options) submitGroupOpsWebhook(cmd *cobra.Command, origin string, body []byte, event string) (map[string]any, error) {
	reference, targets, messages, err := validateWebhookBody(body)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"operation_id": "client.groupops.webhook.send", "origin": origin, "event_digest": webhookDigest([]byte(event)), "request_digest": webhookDigest(body), "target_count": targets, "message_count": messages, "delivery_proven": false}
	secret, _, err := loadWebhookSecret(origin)
	if err != nil {
		return nil, err
	}
	if secret == "" {
		return nil, fail("authentication", "configure this origin's webhook secret or provide origin-bound webhook environment credentials", 3)
	}
	client, err := NewClient(origin, Credential{}, o.Timeout)
	if err != nil {
		return nil, err
	}
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + event + "\n"))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	request, err := http.NewRequestWithContext(ctx(cmd), http.MethodPost, origin+"/api/automation/group-ops/webhooks/"+reference, bytes.NewReader(body))
	if err != nil {
		return nil, fail("validation", "could not construct registered webhook request", 2)
	}
	request.GetBody = nil
	request.Close = true
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-AICRM-Client-Id", webhookClientID)
	request.Header.Set("X-AICRM-Timestamp", timestamp)
	request.Header.Set("X-AICRM-Event-Id", event)
	request.Header.Set("X-AICRM-Signature", signature)
	unknown := func(status int) error {
		result["acceptance"] = "unknown"
		return &Failure{Code: "outcome_unknown", Message: "webhook outcome is unknown; inspect original plan executions, retain the original event ID and exact input, and do not automatically resend", Status: status, Exit: 7, Result: result}
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, unknown(0)
	}
	defer response.Body.Close()
	result["http_status"] = response.StatusCode
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, &Failure{Code: "redirect_refused", Message: "webhook redirect refused; signed credentials were not forwarded", Status: response.StatusCode, Exit: 4, Result: result}
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, unknown(response.StatusCode)
	}
	var value map[string]any
	if !strings.Contains(response.Header.Get("Content-Type"), "json") || StrictJSON(raw, &value) != nil {
		return nil, unknown(response.StatusCode)
	}
	if response.StatusCode != http.StatusAccepted {
		// Only the published owner error envelope is treated as a proven rejection.
		// Arbitrary proxy/5xx text cannot prove whether acceptance occurred.
		errorValue, _ := value["error"].(map[string]any)
		code, _ := errorValue["code"].(string)
		exit, expectedStatus := webhookError(code)
		if value["ok"] != false || value["real_external_call_executed"] != false || value["provider_execution_eligible"] != false || expectedStatus != response.StatusCode {
			return nil, unknown(response.StatusCode)
		}
		result["acceptance"] = "rejected"
		return nil, &Failure{Code: code, Message: "webhook owner rejected the request; consult schema webhook and the usage manual", Status: response.StatusCode, Exit: exit, Result: result}
	}
	run, _ := value["run"].(map[string]any)
	runID, _ := run["run_id"].(string)
	id, parseErr := strconv.ParseInt(runID, 10, 64)
	if value["ok"] == false || parseErr != nil || id < 1 {
		return nil, unknown(response.StatusCode)
	}
	result["acceptance"] = "accepted"
	result["business_success"] = "accepted_await_employee_and_delivery_readback"
	result["data"] = redactWebhookResult(value, secret, signature)
	return result, nil
}

func webhookError(code string) (int, int) {
	switch code {
	case "invalid_request", "miniprogram_cover_unsupported":
		return 2, 400
	case "protocol_authentication_failed":
		return 3, 401
	case "plan_not_found":
		return 2, 404
	case "idempotency_conflict", "operations_conflict":
		return 5, 409
	case "provider_disabled", "miniprogram_cover_unavailable", "protocol_auth_unavailable", "group_ops_unavailable":
		return 1, 503
	}
	return 7, 0
}

func redactWebhookResult(value any, secret, signature string) any {
	switch value := value.(type) {
	case string:
		for _, sensitive := range []string{secret, signature, strings.TrimPrefix(signature, "sha256=")} {
			value = strings.ReplaceAll(value, sensitive, "[REDACTED]")
		}
		return value
	case map[string]any:
		clean := make(map[string]any, len(value))
		for name, item := range value {
			clean[redactWebhookResult(name, secret, signature).(string)] = redactWebhookResult(item, secret, signature)
		}
		return clean
	case []any:
		clean := make([]any, len(value))
		for index, item := range value {
			clean[index] = redactWebhookResult(item, secret, signature)
		}
		return clean
	default:
		return value
	}
}

func groupOpsWebhookSchema() map[string]any {
	definitions := map[string]any{"PositiveID": catalog.Load().Schemas["PositiveID"]}
	for name, value := range catalog.Load().Schemas {
		if strings.HasPrefix(name, "GroupOpsWebhook") {
			definitions[name] = value
		}
	}
	return map[string]any{"command": "aicrm-cli group-ops webhook send", "plan_command": "aicrm-cli group-ops webhook push --plan-id ID --text TEXT --miniprogram-id ID --all-bound-groups", "method": "POST", "path": "/api/automation/group-ops/webhooks/{webhook_reference}", "authentication": "HMAC-SHA256", "client_id": webhookClientID, "headers": []string{"X-AICRM-Client-Id", "X-AICRM-Timestamp", "X-AICRM-Event-Id", "X-AICRM-Signature"}, "signed_bytes": "timestamp + newline + event_id + newline + original JSON bytes", "request_schema": map[string]any{"$ref": "#/components/schemas/GroupOpsWebhookInboundRequest"}, "definitions": definitions, "body_limit_bytes": webhookBodyLimit, "rules": []string{"targets must belong to the active webhook plan; server validates", "optional text first; at most nine attachments and one miniprogram", "retain original event ID and exact request bytes for recovery", "no automatic write retries; 202 is acceptance, not delivery", "employee confirmation is required by the current WeCom provider"}}
}
