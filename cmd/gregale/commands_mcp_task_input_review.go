package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
	if submissionKey != "" {
		task, exchange, err := client.GetTaskBySubmissionKey(ctx, submissionKey)
		if err != nil {
			return mcpTaskMetadataReadFailure("", exchange.HTTPStatus, err)
		}
		taskID = task.TaskID
	}
	task, exchange, err := client.GetTask(ctx, taskID)
	if err != nil {
		return mcpTaskMetadataReadFailure(taskID, exchange.HTTPStatus, err)
	}
	if err := writeMCPTaskInputTemplate(path, client.Endpoint, task); err != nil {
		return printErr("MCP Task input template", err)
	}
	return jsonOut(writeJSON(mcpTaskSnapshot(task)))
}

func writeMCPTaskInputTemplate(path, endpoint string, task mcphosting.Task) error {
	forms, err := task.InputForms()
	if err != nil {
		return err
	}
	review := mcpTaskInputReview{Version: 1, EndpointSHA256: mcpTaskReceiptEndpoint(endpoint),
		TaskID: task.TaskID, Forms: make(map[string]mcpReviewedForm, len(forms))}
	for _, form := range forms {
		digest, err := mcpFormDigest(form.Message, form.Schema)
		if err != nil {
			return errors.New("MCP Task form could not be fingerprinted")
		}
		review.Forms[form.ID] = mcpReviewedForm{Message: form.Message, Schema: form.Schema, FormSHA256: digest}
	}
	body, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return err
	}
	if len(body) >= mcpTaskInputReviewMaxBytes {
		return errors.New("MCP Task input template exceeds 1 MiB")
	}
	if err := publishCustomerOperationReceipt(path, append(body, '\n')); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("MCP Task input template already exists")
		}
		return err
	}
	return nil
}

func readMCPTaskInputReview(path, endpoint string) (mcpTaskInputReview, error) {
	var review mcpTaskInputReview
	raw, err := readCustomerOperationPrivateReceiptBound(path, mcpTaskInputReviewMaxBytes)
	if err != nil {
		return review, err
	}
	canonical, err := operations.CanonicalJSON(raw)
	if err != nil {
		return review, errors.New("invalid MCP Task input review JSON")
	}
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(&review); err != nil || review.Version != 1 || review.TaskID == "" || len(review.Forms) == 0 {
		return review, errors.New("invalid MCP Task input review")
	}
	if review.EndpointSHA256 != mcpTaskReceiptEndpoint(endpoint) {
		return review, errors.New("MCP Task input review does not match the selected endpoint")
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
