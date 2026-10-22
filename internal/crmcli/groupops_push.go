package crmcli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"unicode/utf8"

	catalog "github.com/qianlan33333-png/aicrm-cli/internal/clicatalog/port"
	"github.com/spf13/cobra"
)

type planPushInput struct {
	PlanID         int64
	Text           string
	MiniProgramID  int64
	AllBoundGroups bool
	EventID        string
	DryRun         bool
}

type planPushBody struct {
	Reference string            `json:"webhook_reference"`
	Targets   []string          `json:"target_chat_references"`
	Messages  []planPushMessage `json:"messages"`
}
type planPushMessage struct {
	Type          string `json:"type"`
	Text          string `json:"text,omitempty"`
	MiniProgramID int64  `json:"miniprogram_id,omitempty"`
}
type planPushSnapshot struct {
	Origin   string `json:"origin"`
	EventID  string `json:"event_id"`
	PlanID   int64  `json:"plan_id"`
	Revision int64  `json:"plan_revision"`
	// Base64 preserves the precise wire bytes across JSON journal encoding.
	Request []byte `json:"request_bytes"`
}
type planPushGroup struct {
	Reference string `json:"asset_reference"`
}
type planPushDetail struct {
	Plan struct {
		ID       string `json:"plan_id"`
		Type     string `json:"plan_type"`
		Status   string `json:"status"`
		Revision int64  `json:"revision"`
	} `json:"plan"`
	Groups  []planPushGroup `json:"group_assets"`
	Webhook struct {
		Reference  string `json:"reference"`
		Configured bool   `json:"configured"`
	} `json:"webhook_descriptor"`
}

func groupOpsWebhookPushCommand(o *Options) *cobra.Command {
	var input planPushInput
	command := &cobra.Command{Use: "push", Args: cobra.NoArgs, Short: "push text and a stored mini-program to all groups bound to one plan", RunE: func(cmd *cobra.Command, _ []string) error { return o.pushGroupOpsPlan(cmd, input) }}
	flags := command.Flags()
	flags.Int64Var(&input.PlanID, "plan-id", 0, "active Webhook plan ID")
	flags.StringVar(&input.Text, "text", "", "text sent before the mini-program card")
	flags.Int64Var(&input.MiniProgramID, "miniprogram-id", 0, "positive ID of an enabled card in the material library")
	flags.BoolVar(&input.AllBoundGroups, "all-bound-groups", false, "explicitly target every group bound to this plan")
	flags.StringVar(&input.EventID, "event-id", "", "stable event for automation or recovery; otherwise create a new event and persist its request")
	flags.BoolVar(&input.DryRun, "dry-run", false, "read the plan and all binding pages, persist a preview, but do not sign or POST")
	return command
}

func (o *Options) pushGroupOpsPlan(cmd *cobra.Command, input planPushInput) error {
	if input.PlanID < 1 || input.MiniProgramID < 1 || !validWebhookText(input.Text, 1000) || !utf8.ValidString(input.Text) || !input.AllBoundGroups {
		return fail("validation", "push requires positive --plan-id, --miniprogram-id, valid --text and explicit --all-bound-groups", 2)
	}
	if input.EventID != "" && !validWebhookEvent(input.EventID) {
		return fail("validation", "--event-id requires 16-256 visible ASCII characters without spaces", 2)
	}
	origin, err := o.origin()
	if err != nil {
		return err
	}
	event := input.EventID
	if event == "" {
		random := make([]byte, 16)
		if _, err = rand.Read(random); err != nil {
			return fail("io", "could not create a new push event", 1)
		}
		event = "groupops-" + strconv.FormatInt(input.PlanID, 10) + "-" + hex.EncodeToString(random)
	}
	path, err := planPushSnapshotPath(origin, event)
	if err != nil {
		return err
	}
	snapshot, exists, err := loadPlanPushSnapshot(path)
	if err != nil {
		return err
	}
	readNetwork := false
	if exists {
		var body planPushBody
		if len(snapshot.Request) > webhookBodyLimit || !utf8.Valid(snapshot.Request) || snapshot.Origin != origin || snapshot.EventID != event || snapshot.PlanID != input.PlanID || snapshot.Revision < 1 || json.Unmarshal(snapshot.Request, &body) != nil ||
			len(body.Messages) != 2 || body.Messages[0].Type != "text" || body.Messages[0].Text != input.Text || body.Messages[1].Type != "miniprogram" || body.Messages[1].MiniProgramID != input.MiniProgramID {
			return fail("idempotency_conflict", "this event's saved request differs from the supplied plan, text or mini-program; preserve the original request", 5)
		}
		if _, _, _, err = validateWebhookBody(snapshot.Request); err != nil {
			return fail("validation", "saved push request is invalid; no request sent", 2)
		}
	} else {
		client, e := o.client()
		if e != nil {
			return e
		}
		body, revision, e := o.preparePlanPush(cmd, client, input)
		if e != nil {
			return e
		}
		readNetwork = true
		snapshot = planPushSnapshot{Origin: origin, EventID: event, PlanID: input.PlanID, Revision: revision, Request: body}
		if err = savePlanPushSnapshot(path, snapshot); err != nil {
			return err
		}
	}
	_, targets, messages, err := validateWebhookBody(snapshot.Request)
	if err != nil {
		return err
	}
	metadata := map[string]any{"operation_id": "client.groupops.webhook.push", "origin": origin, "plan_id": input.PlanID, "plan_revision": snapshot.Revision, "all_bound_groups": true, "event_id": event, "request_snapshot": path, "snapshot_reused": exists, "event_digest": webhookDigest([]byte(event)), "request_digest": webhookDigest(snapshot.Request), "target_count": targets, "message_count": messages, "delivery_proven": false}
	if input.DryRun {
		metadata["dry_run"] = true
		metadata["network_request_sent"] = readNetwork
		metadata["write_request_sent"] = false
		metadata["authorization_and_bound_targets_verified_at_snapshot"] = true
		return o.print(metadata)
	}
	// Emit recovery information before the POST, even if the final response is lost.
	if err = json.NewEncoder(o.Err).Encode(map[string]any{"event_id": event, "request_snapshot": path, "plan_id": input.PlanID, "target_count": targets}); err != nil {
		return fail("io", "could not report recovery information; no push sent", 1)
	}
	result, err := o.submitGroupOpsWebhook(cmd, origin, snapshot.Request, event)
	if err != nil {
		var failure *Failure
		if errors.As(err, &failure) {
			if data, ok := failure.Result.(map[string]any); ok {
				for k, v := range data {
					metadata[k] = v
				}
			}
			metadata["operation_id"] = "client.groupops.webhook.push"
			failure.Result = metadata
		}
		return err
	}
	for k, v := range result {
		metadata[k] = v
	}
	metadata["operation_id"] = "client.groupops.webhook.push"
	return o.print(metadata)
}

func readPlanPushOperation(cmd *cobra.Command, client *Client, id string, input CallInput, out any) error {
	operation, ok := catalog.Find(id)
	if !ok || operation.Binding == nil || operation.Binding.Method != "GET" {
		return fail("unavailable", "required plan read operation is not registered", 4)
	}
	response, err := client.Call(ctx(cmd), operation, input)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fail("protocol", "plan read did not return HTTP 200; no push sent", 1)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fail("io", "could not read the complete plan response; no push sent", 1)
	}
	if err = StrictJSON(body, out); err != nil {
		return fail("protocol", "invalid plan response; no push sent", 1)
	}
	return nil
}

func (o *Options) preparePlanPush(cmd *cobra.Command, client *Client, input planPushInput) ([]byte, int64, error) {
	if err := client.authorizePlanPushReader(ctx(cmd)); err != nil {
		return nil, 0, err
	}
	parameters := map[string]string{"plan_id": strconv.FormatInt(input.PlanID, 10)}
	var before planPushDetail
	if err := readPlanPushOperation(cmd, client, "admin.getGroupOpsPlan", CallInput{Parameters: parameters}, &before); err != nil {
		return nil, 0, err
	}
	if before.Plan.ID != parameters["plan_id"] || before.Plan.Type != "webhook" || before.Plan.Status != "active" || before.Plan.Revision < 1 || !before.Webhook.Configured {
		return nil, 0, fail("validation", "plan must be the selected active, configured Webhook plan", 2)
	}
	targets := []string{}
	seen := map[string]bool{}
	offset, total := 0, -1
	for {
		var page struct {
			Items  []planPushGroup `json:"items"`
			Limit  int             `json:"limit"`
			Offset int             `json:"offset"`
			Total  int             `json:"total"`
			More   bool            `json:"has_more"`
		}
		err := readPlanPushOperation(cmd, client, "admin.listGroupOpsPlanGroups", CallInput{Parameters: parameters, Query: map[string]string{"limit": "100", "offset": strconv.Itoa(offset)}}, &page)
		if err != nil {
			return nil, 0, err
		}
		if total == -1 {
			total = page.Total
		}
		if page.Offset != offset || page.Limit < 1 || page.Limit > 100 || page.Total != total || total < 0 || len(page.Items) > page.Limit || len(page.Items) == 0 {
			return nil, 0, fail("protocol", "empty, incomplete or changing plan bindings; no push sent", 2)
		}
		for _, group := range page.Items {
			if !webhookOpaque.MatchString(group.Reference) || seen[group.Reference] {
				return nil, 0, fail("protocol", "invalid or repeated group binding; no push sent", 2)
			}
			seen[group.Reference] = true
			targets = append(targets, group.Reference)
		}
		offset += len(page.Items)
		if offset > total || page.More != (offset < total) {
			return nil, 0, fail("protocol", "incomplete plan binding pagination; no push sent", 2)
		}
		if !page.More {
			break
		}
	}
	var after planPushDetail
	if err := readPlanPushOperation(cmd, client, "admin.getGroupOpsPlan", CallInput{Parameters: parameters}, &after); err != nil {
		return nil, 0, err
	}
	sameGroups := func(groups []planPushGroup) bool {
		if len(groups) != len(targets) {
			return false
		}
		unique := map[string]bool{}
		for _, g := range groups {
			if !seen[g.Reference] || unique[g.Reference] {
				return false
			}
			unique[g.Reference] = true
		}
		return true
	}
	if after.Plan != before.Plan || after.Webhook != before.Webhook || !sameGroups(before.Groups) || !sameGroups(after.Groups) {
		return nil, 0, fail("operations_conflict", "plan or its bindings changed while preparing the push; no push sent", 5)
	}
	sort.Strings(targets)
	body, err := json.Marshal(planPushBody{Reference: before.Webhook.Reference, Targets: targets, Messages: []planPushMessage{{Type: "text", Text: input.Text}, {Type: "miniprogram", MiniProgramID: input.MiniProgramID}}})
	if err != nil {
		return nil, 0, err
	}
	if len(body) > webhookBodyLimit {
		return nil, 0, fail("validation", "all bound targets exceed the server's 64 KiB request limit; no partial push sent", 2)
	}
	if _, _, _, err = validateWebhookBody(body); err != nil {
		return nil, 0, err
	}
	return body, before.Plan.Revision, nil
}

func planPushSnapshotPath(origin, event string) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "webhook-requests", webhookDigest([]byte(origin))+"-"+webhookDigest([]byte(event))+".json"), nil
}

func loadPlanPushSnapshot(path string) (planPushSnapshot, bool, error) {
	var snapshot planPushSnapshot
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, false, nil
	}
	if err != nil || !stat.Mode().IsRegular() {
		return snapshot, false, fail("io", "could not read a regular push snapshot; no push sent", 1)
	}
	file, err := os.Open(path)
	if err != nil {
		return snapshot, false, fail("io", "could not open push snapshot; no push sent", 1)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 2*webhookBodyLimit+1))
	if err != nil || len(raw) > 2*webhookBodyLimit || StrictJSON(raw, &snapshot) != nil {
		return snapshot, false, fail("io", "push snapshot is invalid or incomplete; no push sent", 1)
	}
	return snapshot, true, nil
}

func savePlanPushSnapshot(path string, snapshot planPushSnapshot) error {
	failSave := func() error { return fail("io", "could not persist the exact push snapshot; no push sent", 1) }
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return failSave()
	}
	stat, err := os.Lstat(directory)
	if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
		return failSave()
	}
	file, err := os.CreateTemp(directory, ".pending-*")
	if err != nil {
		return failSave()
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		err = json.NewEncoder(file).Encode(snapshot)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return failSave()
	}
	// Atomic, create-only publication prevents two callers replacing one event.
	if err = os.Link(file.Name(), path); err != nil {
		return failSave()
	}
	dir, err := os.Open(directory)
	if err != nil {
		return failSave()
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return failSave()
	}
	return nil
}
