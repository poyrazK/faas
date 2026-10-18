package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

// --app selects the hosted management API and the normal Gregale login.
// Every mutation preserves a caller-visible identity; no automatic retries.
func cmdDataRepairManaged(verb string, args []string) int {
	fs := newFlagSet("data-repair "+verb, flag.ContinueOnError)
	app := fs.String("app", "", "application slug in the hosted management API")
	digest := fs.String("digest", "", "exact reviewed plan digest")
	id := fs.String("repair-operation", "", "stable UUID reserved before preparing a repair")
	source := fs.String("source", "", "original source UUID for read-only retained get or verify")
	fs.String("environment", "", "configured environment for retained source history")
	fs.String("customer", "", "configured customer for retained source history")
	yes := fs.Bool("yes", false, "confirm the exact plan approval or application")
	flags, pos := splitArgsForFlags(args, "yes")
	if fs.Parse(flags) != nil || len(pos) != 1 || *app == "" || !dataRepairUUID(pos[0]) {
		return 1
	}
	scope := url.Values{}
	invalid := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "environment" || f.Name == "customer" {
			scope.Set(f.Name, f.Value.String())
		}
		if f.Name == "source" && !dataRepairUUID(*source) {
			invalid = true
		}
	})
	if invalid || (*source != "" || len(scope) > 0) && (*source == "" || verb != "get" && verb != "verify" || *id != "" || *yes) {
		return printErr("Use --source and hosted scope flags only for read-only get or verify", datarepair.ErrInvalid)
	}
	if (verb == "approve" || verb == "reject" || verb == "apply" || verb == "replace" || verb == "resolve") && !*yes {
		return printErr("Approval, apply, replacement and resolution require --yes", datarepair.ErrInvalid)
	}
	if verb != "plan" && verb != "get" && *digest == "" {
		return printErr("Use the exact reviewed --digest", datarepair.ErrInvalid)
	}
	if verb == "plan" && !dataRepairUUID(*id) {
		return printErr("Save a UUID and pass --repair-operation before preparing the hosted plan", datarepair.ErrInvalid)
	}
	if verb == "replace" && (!dataRepairUUID(*id) || *id == pos[0]) {
		return printErr("Save a new UUID and pass --repair-operation for the replacement", datarepair.ErrInvalid)
	}
	if (verb == "apply" || verb == "resolve") && *id != "" && *id != pos[0] {
		return printErr("The repair operation ID must match the hosted operation", datarepair.ErrInvalid)
	}
	u, err := repairEndpoint(apiBase())
	if err != nil {
		return printErr("Use an HTTPS management API or local loopback", err)
	}
	token := loadToken()
	if token == "" {
		return printErr("Not logged in", datarepair.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.DataRepairTimeoutSeconds+10)*time.Second)
	defer cancel()
	if *source != "" {
		base := strings.TrimRight(u.String(), "/") + "/v1/apps/" + url.PathEscape(*app) + "/data-repair"
		return cmdDataRepairHistorical(ctx, verb, base, token, *source, pos[0], *digest, scope)
	}
	base := strings.TrimRight(u.String(), "/") + "/v1/apps/" + url.PathEscape(*app) + "/data-repair/operations"
	raw, err := managedRepairCommand(ctx, verb, base, token, pos[0], *id, *digest)
	if err != nil {
		return printErr("Managed repair request failed; retain its ID and inspect its database receipt", err)
	}
	if verb == "validate" {
		return printDataRepairValidation(raw, pos[0], *digest)
	}
	if verb == "verify" {
		return printDataRepairVerification(raw, pos[0], *digest, true)
	}
	if verb == "replace" {
		var response repairapi.ManagedResponse
		if json.Unmarshal(raw, &response) != nil || response.Operation.ID != *id || !validReplacementCLI(response.Plan, pos[0], *digest, *id) {
			return printErr("Replacement response did not match its saved identity", datarepair.ErrUnavailable)
		}
	}
	if verb == "resolve" {
		var response repairapi.ManagedResponse
		if json.Unmarshal(raw, &response) != nil || response.Operation.ID != pos[0] || response.Plan.Scope.Validate() != nil || !response.Plan.ResolvedApply(pos[0], *digest, pos[0]) {
			return printErr("Source did not provide a matching receipt or permanent closure", datarepair.ErrUnavailable)
		}
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") != nil {
		return printErr("Invalid managed repair response", datarepair.ErrUnavailable)
	}
	if _, err = fmt.Fprintln(osStdout, pretty.String()); err != nil {
		return printErr("Cannot write repair result", err)
	}
	return 0
}

func dataRepairUUID(raw string) bool {
	id, err := uuid.Parse(raw)
	return err == nil && id != uuid.Nil && id.String() == raw
}

func managedRepairCommand(ctx context.Context, verb, base, token, target, id, digest string) ([]byte, error) {
	if verb == "plan" {
		return managedRepairRequest(ctx, base, token, map[string]string{"source_operation_id": target}, id)
	}
	if verb != "get" && verb != "validate" && verb != "verify" && verb != "replace" && verb != "rehearse" && verb != "cancel-rehearsal" && verb != "approve" && verb != "reject" && verb != "apply" && verb != "reconcile" && verb != "resolve" {
		return nil, datarepair.ErrInvalid
	}
	endpoint := base + "/" + url.PathEscape(target)
	if verb == "validate" || verb == "verify" {
		return managedRepairRequest(ctx, endpoint+"/"+verb, token, map[string]string{"digest": digest}, "")
	}
	raw, err := managedRepairRequest(ctx, endpoint, token, nil, "")
	if err != nil || verb == "get" {
		return raw, err
	}
	var current repairapi.ManagedResponse
	if json.Unmarshal(raw, &current) != nil || current.Plan.Digest != digest || current.Operation.ID != target {
		return nil, datarepair.ErrConflict
	}
	if verb == "replace" {
		return managedRepairRequest(ctx, endpoint+"/replace", token, map[string]any{"digest": digest, "expected_sequence": current.Operation.LatestSequence}, id)
	}
	if verb == "approve" || verb == "reject" {
		decision := api.OperationApprovalApproved
		if verb == "reject" {
			decision = api.OperationApprovalRejected
		}
		// Construct the account endpoint locally; never follow a returned URL
		// carrying the user's credential to another origin.
		approval := strings.Replace(base, "/data-repair/operations", "/operations", 1) + "/" + url.PathEscape(target) + "/approvals/repair"
		return managedRepairRequest(ctx, approval, token, api.OperationApprovalRequest{ExpectedGeneration: current.Operation.Generation, Decision: decision}, "")
	}
	return managedRepairRequest(ctx, endpoint+"/"+verb, token, map[string]any{"digest": digest, "expected_sequence": current.Operation.LatestSequence}, "")
}

func managedRepairRequest(ctx context.Context, endpoint, token string, body any, id string) ([]byte, error) {
	return managedRepairRequestLimit(ctx, endpoint, token, body, id, api.DataRepairMaxPlanBytes)
}

func managedRepairRequestLimit(ctx context.Context, endpoint, token string, body any, id string, limit int) ([]byte, error) {
	method := http.MethodGet
	var raw []byte
	if body != nil {
		method = http.MethodPost
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, datarepair.ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if id != "" {
		req.Header.Set("Idempotency-Key", id)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, datarepair.ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err = io.ReadAll(io.LimitReader(resp.Body, int64(limit+1)))
	if err != nil || len(raw) > limit {
		return nil, datarepair.ErrUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		var p struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(raw, &p) == nil && p.Detail != "" {
			return nil, fmt.Errorf("HTTP %d (%s): %s", resp.StatusCode, p.Code, p.Detail)
		}
		return nil, fmt.Errorf("HTTP %d: repair unavailable", resp.StatusCode)
	}
	return raw, nil
}
