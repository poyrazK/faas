package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/operations"
)

const mcpTaskInputReviewMaxBytes = 1 << 20

var errMCPTaskInputReviewMismatch = errors.New("reviewed Task input does not match the pending form")

type mcpTaskInputReview struct {
	Version        int                        `json:"version"`
	EndpointSHA256 string                     `json:"endpointSha256"`
	TaskID         string                     `json:"taskId"`
	Revision       string                     `json:"reviewRevision,omitempty"`
	Forms          map[string]mcpReviewedForm `json:"forms"`
}

type mcpReviewedForm struct {
	Message    string                    `json:"message"`
	Schema     map[string]any            `json:"schema"`
	FormSHA256 string                    `json:"formSha256"`
	Response   *mcphosting.InputResponse `json:"response"`
}

func mcpFormDigest(message string, schema map[string]any) (string, error) {
	raw, err := json.Marshal(struct {
		Message string         `json:"message"`
		Schema  map[string]any `json:"schema"`
	}{message, schema})
	if err != nil {
		return "", err
	}
	canonical, err := operations.CanonicalJSON(raw)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(canonical)), nil
}

func runMCPTaskInputTemplate(ctx context.Context, client *mcphosting.Client, taskID, submissionKey, path string) int {
	var task mcphosting.Task
	var exchange mcphosting.Exchange
	var revision string
	var err error
	if submissionKey != "" {
		task, exchange, err = client.GetTaskBySubmissionKey(ctx, submissionKey)
		if err != nil {
			return mcpTaskMetadataReadFailure("", exchange.HTTPStatus, err)
		}
		taskID = task.TaskID
		task, exchange, err = client.GetTask(ctx, taskID)
	} else {
		var draft mcphosting.TaskInputReview
		draft, exchange, err = client.GetTaskInputReview(ctx, taskID)
		task, revision = draft.Task, draft.Revision
		if errors.Is(err, mcphosting.ErrTaskInputReviewUnsupported) {
			task, exchange, err = client.GetTask(ctx, taskID)
		}
	}
	if err != nil {
		return mcpTaskMetadataReadFailure(taskID, exchange.HTTPStatus, err)
	}
	if err := writeMCPTaskInputTemplate(path, client.Endpoint, task, revision); err != nil {
		return printErr("MCP Task input template", err)
	}
	return jsonOut(writeJSON(mcpTaskSnapshot(task)))
}

func writeMCPTaskInputTemplate(path, endpoint string, task mcphosting.Task, revision string) error {
	review, err := newMCPTaskInputTemplate(endpoint, task, revision)
	if err != nil {
		return err
	}
	body, err := encodeMCPTaskInputTemplate(review)
	if err != nil {
		return err
	}
	if err := publishCustomerOperationReceipt(path, body); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("MCP Task input template already exists")
		}
		return err
	}
	return nil
}

func newMCPTaskInputTemplate(endpoint string, task mcphosting.Task, revision string) (mcpTaskInputReview, error) {
	var review mcpTaskInputReview
	forms, err := task.InputForms()
	if err != nil {
		return review, err
	}
	review = mcpTaskInputReview{Version: 1, EndpointSHA256: mcpTaskReceiptEndpoint(endpoint),
		TaskID: task.TaskID, Revision: revision, Forms: make(map[string]mcpReviewedForm, len(forms))}
	if revision != "" {
		review.Version = 2
	}
	for _, form := range forms {
		digest, err := mcpFormDigest(form.Message, form.Schema)
		if err != nil {
			return review, errors.New("MCP Task form could not be fingerprinted")
		}
		review.Forms[form.ID] = mcpReviewedForm{Message: form.Message, Schema: form.Schema, FormSHA256: digest}
	}
	return review, nil
}

func encodeMCPTaskInputTemplate(review mcpTaskInputReview) ([]byte, error) {
	body, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(body) >= mcpTaskInputReviewMaxBytes {
		return nil, errors.New("MCP Task input template exceeds 1 MiB")
	}
	return append(body, '\n'), nil
}

func readMCPTaskInputReview(path, endpoint string) (mcpTaskInputReview, error) {
	raw, err := readCustomerOperationPrivateReceiptBound(path, mcpTaskInputReviewMaxBytes)
	if err != nil {
		return mcpTaskInputReview{}, err
	}
	return decodeMCPTaskInputReview(raw, endpoint)
}

func decodeMCPTaskInputReview(raw []byte, endpoint string) (mcpTaskInputReview, error) {
	var review mcpTaskInputReview
	canonical, err := operations.CanonicalJSON(raw)
	if err != nil {
		return review, errors.New("invalid MCP Task input review JSON")
	}
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(&review); err != nil || review.TaskID == "" || len(review.Forms) == 0 {
		return review, errors.New("invalid MCP Task input review")
	}
	legacy := review.Version == 1 && review.Revision == ""
	guarded := review.Version == 2 && review.Revision != ""
	if !legacy && !guarded {
		return review, errors.New("invalid MCP Task input review version or revision")
	}
	if review.EndpointSHA256 != mcpTaskReceiptEndpoint(endpoint) {
		// Group exports use the canonical endpoint saved by the Node SDK.
		canonicalEndpoint, err := mcpTaskGroupEndpoint(endpoint)
		if err != nil || review.Revision == "" || review.EndpointSHA256 != mcpTaskReceiptEndpoint(canonicalEndpoint) {
			return review, errors.New("MCP Task input review does not match the selected endpoint")
		}
	}
	for _, form := range review.Forms {
		digest, err := mcpFormDigest(form.Message, form.Schema)
		if err != nil || digest != form.FormSHA256 {
			return review, errors.New("MCP Task input review contains a changed form")
		}
	}
	return review, nil
}

func (review mcpTaskInputReview) respond(_ context.Context, live mcphosting.InputRequest) (mcphosting.InputResponse, error) {
	form, ok := review.Forms[live.ID]
	digest, err := mcpFormDigest(live.Message, live.Schema)
	if !ok || err != nil || form.FormSHA256 != digest {
		return mcphosting.InputResponse{}, errMCPTaskInputReviewMismatch
	}
	if form.Response == nil {
		return mcphosting.InputResponse{}, fmt.Errorf("%w: an explicit response is required for request %q", errMCPTaskInputReviewMismatch, live.ID)
	}
	return *form.Response, nil
}

func runMCPTaskInputResponse(ctx context.Context, client *mcphosting.Client, submissionKey string, review mcpTaskInputReview) int {
	task, exchange, err := client.GetTaskBySubmissionKey(ctx, submissionKey)
	if err != nil {
		return mcpTaskMetadataReadFailure("", exchange.HTTPStatus, err)
	}
	if task.TaskID != review.TaskID {
		return printErr("MCP Task input review", errors.New("MCP Task input review belongs to a different Task"))
	}
	if task.Status != "input_required" {
		return jsonOut(writeJSON(mcpTaskSnapshot(task)))
	}
	task, exchange, err = client.GetTask(ctx, task.TaskID)
	if err != nil {
		return mcpTaskMetadataReadFailure(review.TaskID, exchange.HTTPStatus, err)
	}
	current, err := client.RespondToTask(ctx, task, review.respond)
	var unconfirmed *mcphosting.TaskInputUpdateUnconfirmedError
	if errors.As(err, &unconfirmed) {
		return mcpTaskInputUpdateStatus(unconfirmed)
	}
	if err != nil {
		return printErr("MCP Task input response", err)
	}
	return jsonOut(writeJSON(mcpTaskSnapshot(current)))
}

func runMCPTaskInputResponseByID(ctx context.Context, client *mcphosting.Client, taskID string, review mcpTaskInputReview) int {
	if taskID != review.TaskID {
		return printErr("MCP Task input review", errors.New("MCP Task input review belongs to a different Task"))
	}
	responses, err := reviewedMCPTaskInputResponses(review)
	if err != nil {
		return printErr("MCP Task input review", err)
	}
	draft, exchange, err := client.GetTaskInputReview(ctx, taskID)
	if err != nil {
		return mcpTaskMetadataReadFailure(taskID, exchange.HTTPStatus, err)
	}
	current, err := applyMCPTaskReviewedInput(ctx, client, draft, review, responses)
	if errors.Is(err, errMCPTaskInputReviewMismatch) {
		return printErr("MCP Task input review", err)
	}
	return printMCPTaskInputResponseByID(current, err)
}

func reviewedMCPTaskInputResponses(review mcpTaskInputReview) (map[string]mcphosting.InputResponse, error) {
	if review.Revision == "" {
		return nil, errors.New("export a new input template by Task ID before submitting")
	}
	responses := make(map[string]mcphosting.InputResponse, len(review.Forms))
	for id, form := range review.Forms {
		if form.Response == nil {
			return nil, errors.New("each reviewed form requires an explicit response")
		}
		responses[id] = *form.Response
	}
	return responses, nil
}

func applyMCPTaskReviewedInput(ctx context.Context, client *mcphosting.Client, draft mcphosting.TaskInputReview, review mcpTaskInputReview, responses map[string]mcphosting.InputResponse) (mcphosting.Task, error) {
	if draft.Revision != review.Revision {
		// A prior invocation may have applied this exact review before exiting.
		current, err := client.CheckTaskInputReview(ctx, review.TaskID, review.Revision, responses)
		var unconfirmed *mcphosting.TaskInputUpdateUnconfirmedError
		if errors.As(err, &unconfirmed) && unconfirmed.Unwrap() == nil {
			err = mcphosting.ErrTaskInputReviewStale
		}
		return current, err
	}
	forms, err := draft.Task.InputForms()
	if err != nil || len(forms) != len(review.Forms) {
		return mcphosting.Task{}, errMCPTaskInputReviewMismatch
	}
	for _, form := range forms {
		if _, err := review.respond(ctx, form); err != nil {
			return mcphosting.Task{}, err
		}
	}
	return client.ApplyTaskInputReview(ctx, review.TaskID, review.Revision, responses)
}

func printMCPTaskInputResponseByID(current mcphosting.Task, err error) int {
	outcome := "applied"
	var unconfirmed *mcphosting.TaskInputUpdateUnconfirmedError
	if errors.As(err, &unconfirmed) {
		outcome, current = "unconfirmed", unconfirmed.Task
	} else if err != nil {
		return printErr("MCP Task input response", err)
	}
	snapshot := mcpTaskSnapshot(current)
	if unconfirmed != nil {
		if snapshot.Status == "" {
			snapshot.Status = "unknown"
		}
		snapshot.NextAction = "retry_status"
	}
	code := jsonOut(writeJSON(struct {
		mcpTaskStatusMetadata
		Outcome string `json:"outcome"`
	}{snapshot, outcome}))
	if code == 0 && unconfirmed != nil {
		return exitCodeForStatus(http.StatusServiceUnavailable)
	}
	return code
}
