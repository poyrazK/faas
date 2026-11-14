// Package toolinvocation defines the single-dispatch journal contract. It
// contains no transport, credentials, customer arguments or execution retries.
package toolinvocation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var (
	ErrInvalid  = errors.New("invalid tool invocation")
	ErrConflict = errors.New("tool invocation changed")
)

var tokenVariable = regexp.MustCompile(`^MCP_[A-Za-z0-9_]+$`)

func ValidTokenEnv(mode, name string) bool {
	return mode == "open" && name == "" || mode == "external-oauth" && len(name) <= 128 && tokenVariable.MatchString(name)
}

func CredentialBinding(name string) string {
	if name == "" {
		return ""
	}
	return toolpublication.Digest([]byte(name))
}

func ValidateRecord(record api.ToolInvocationRecord, account, id string) error {
	if d := record.Delegation; d != nil && (!ValidID(d.GrantID) || !toolgrant.ValidResourceID(d.KeyID) || d.ProjectID != "" && !toolgrant.ValidResourceID(d.ProjectID) || (record.Intent.AccessMode != "open" && record.Intent.AccessMode != "external-oauth")) {
		return ErrInvalid
	}
	if account == "" || record.AccountID != account || record.ID != id || !ValidID(id) || ValidateIntent(record.Intent) != nil || ValidateTarget(record.Target) != nil || record.CreatedAt.IsZero() || !record.Deadline.After(record.CreatedAt) {
		return ErrInvalid
	}
	switch record.Status {
	case "queued", "running", "completed", "tool_error", "not_started", "cancelled", "outcome_unknown":
	default:
		return ErrInvalid
	}
	if (record.Status == "running" || record.Status == "completed" || record.Status == "tool_error" || record.Status == "outcome_unknown") && record.DispatchedAt == nil {
		return ErrInvalid
	}
	if (record.Status == "queued" || record.Status == "cancelled" || record.Status == "not_started") && record.DispatchedAt != nil {
		return ErrInvalid
	}
	if record.Outcome != nil && (record.Outcome.Status != record.Status || ValidateOutcome(*record.Outcome) != nil) {
		return ErrInvalid
	}
	if (record.Status == "completed" || record.Status == "tool_error") && record.Outcome == nil {
		return ErrInvalid
	}
	return nil
}

func ValidID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func CanonicalArguments(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if len(raw) > api.ToolInvocationArgsMaxBytes {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil {
		return nil, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrInvalid
	}
	return json.Marshal(value)
}

func ValidateIntent(in api.ToolInvocationIntent) error {
	if !toolpublication.ValidID(in.Version) || in.Name == "" || len(in.Name) > 128 || !toolpublication.ValidID(in.ArgumentsSHA256) || !in.AllowToolExecution ||
		in.TimeoutSeconds < 1 || in.TimeoutSeconds > api.ToolInvocationMaxTimeout {
		return ErrInvalid
	}
	if in.AccessMode == "open" && in.CredentialBindingSHA256 == "" {
		return nil
	}
	if in.AccessMode == "external-oauth" && toolpublication.ValidID(in.CredentialBindingSHA256) {
		return nil
	}
	return ErrInvalid
}

func ValidateTarget(target api.ToolInvocationTarget) error {
	if !api.ValidAppSlug(target.App) || target.AppID == "" || target.DeploymentID == "" || !toolpublication.ValidID(target.DeploymentSourceSHA256) ||
		!toolpublication.ValidID(target.CatalogSHA256) || !toolpublication.ValidID(target.CatalogContractSHA256) || !toolpublication.ValidEndpointPath(target.EndpointPath) {
		return ErrInvalid
	}
	return nil
}

func New(account, id string, in api.ToolInvocationIntent, target api.ToolInvocationTarget, now time.Time) (api.ToolInvocationRecord, error) {
	if account == "" || !ValidID(id) || ValidateIntent(in) != nil || ValidateTarget(target) != nil {
		return api.ToolInvocationRecord{}, ErrInvalid
	}
	return api.ToolInvocationRecord{ID: id, AccountID: account, Intent: in, Target: target, Status: "queued", CreatedAt: now.UTC(), Deadline: now.Add(time.Duration(in.TimeoutSeconds)*time.Second + api.ToolInvocationReportGrace).UTC()}, nil
}

// View never returns an expired grant to dispatch. A queued request was not
// dispatched; a lost running publisher may already have performed side effects.
func View(record api.ToolInvocationRecord, now time.Time) api.ToolInvocationRecord {
	if !now.Before(record.Deadline) {
		if record.Status == "queued" {
			record.Status = "not_started"
		}
		if record.Status == "running" {
			record.Status = "outcome_unknown"
		}
	}
	return record
}

func Terminal(status string) bool { return status != "queued" && status != "running" }

// Change is serialized by the owning store. DispatchGranted is true only for
// the first queued-to-running transition, never on a duplicate request.
func Change(record api.ToolInvocationRecord, action string, out *api.ToolInvocationOutcome, now time.Time) (api.ToolInvocationReceipt, error) {
	result := api.ToolInvocationReceipt{Record: record}
	switch action {
	case "finish":
		if out == nil || ValidateOutcome(*out) != nil {
			return result, ErrInvalid
		}
		if Terminal(record.Status) {
			if record.Outcome != nil && reflect.DeepEqual(*record.Outcome, *out) {
				return result, nil
			}
			return result, ErrConflict
		}
		if !now.Before(record.Deadline) || record.Status == "queued" && out.Status != "not_started" || record.Status == "running" && out.Status == "not_started" {
			return result, ErrConflict
		}
		record.Status, record.Outcome, record.FinishedAt = out.Status, out, &now
	case "cancel":
		if !now.Before(record.Deadline) {
			result.Record = View(record, now)
			return result, nil
		}
		if record.Status == "queued" {
			record.Status = "cancelled"
			record.FinishedAt = &now
		}
		if record.Status == "running" || record.Status == "cancelled" {
			record.CancellationRequested = true
		}
	case "dispatch":
		if record.Status == "queued" && !record.CancellationRequested && now.Before(record.Deadline) {
			record.Status, record.DispatchedAt = "running", &now
			result.DispatchGranted = true
		}
	default:
		return result, ErrInvalid
	}
	result.Record = View(record, now)
	return result, nil
}

func ValidateOutcome(out api.ToolInvocationOutcome) error {
	switch out.Status {
	case "completed", "tool_error":
		if out.Code != "result_received" || !toolpublication.ValidID(out.ResultSHA256) || out.ResultBytes < 1 {
			return ErrInvalid
		}
		if out.ResultOmitted {
			if len(out.Result) != 0 || out.ResultBytes <= api.ToolInvocationResultMaxBytes {
				return ErrInvalid
			}
		} else if len(out.Result) != out.ResultBytes || len(out.Result) > api.ToolInvocationResultMaxBytes || toolpublication.Digest(out.Result) != out.ResultSHA256 || !validResult(out.Result, out.Status == "tool_error") {
			return ErrInvalid
		}
	case "not_started", "outcome_unknown":
		if len(out.Result) != 0 || out.ResultSHA256 != "" || out.ResultBytes != 0 || out.ResultOmitted || out.Redacted {
			return ErrInvalid
		}
		switch out.Code {
		case "preflight_failed", "version_unavailable", "catalog_changed", "transport_error", "timeout", "cancel_requested", "worker_interrupted":
		default:
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validResult(raw []byte, isError bool) bool {
	var result struct {
		Content           *[]json.RawMessage `json:"content"`
		IsError           bool               `json:"isError"`
		StructuredContent json.RawMessage    `json:"structuredContent,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&result) == nil && result.Content != nil && result.IsError == isError
}
