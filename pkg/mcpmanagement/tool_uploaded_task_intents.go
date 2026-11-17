package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/tasktoolsource/sourcecheck"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type prepareUploadedTaskArgs struct {
	planUploadedTaskArgs
	IntentID             string `json:"intent_id" jsonschema:"Caller-chosen lowercase SHA256 identity; reuse this identity after uncertain outcomes"`
	ExpectedReviewSHA256 string `json:"expected_review_sha256" jsonschema:"Exact fingerprint returned by hosted Task source review"`
}
type getUploadedTaskIntentArgs struct {
	IntentID string `json:"intent_id"`
}

func (m *management) hostedTaskSourceAccess() error {
	if err := m.toolBuildingAccess(); err != nil {
		return err
	}
	if !m.remote() {
		return invalid("tool_build_access_denied", "source retention requires a hosted publishing connection")
	}
	return nil
}
func (m *management) getUploadedTaskIntent(ctx context.Context, a getUploadedTaskIntentArgs) (any, error) {
	if err := m.hostedTaskSourceAccess(); err != nil {
		return nil, err
	}
	if !toolpublication.ValidID(a.IntentID) {
		return nil, invalid("invalid_argument", "intent_id must be a lowercase SHA256")
	}
	account, err := m.uploadedTaskAccount(ctx)
	if err != nil {
		return nil, err
	}
	record, err := m.client.GetHostedTaskSourceIntent(ctx, a.IntentID)
	if err != nil {
		return nil, err
	}
	if tasktoolsource.ValidateRecord(record, account, a.IntentID) != nil {
		return nil, invalid("invalid_task_source_receipt", "native Task source receipt is invalid")
	}
	return tasktoolsource.View(record, record.SourceState == "retained", time.Now()), nil
}
func (m *management) prepareUploadedTaskIntent(ctx context.Context, a prepareUploadedTaskArgs) (any, error) {
	if err := m.hostedTaskSourceAccess(); err != nil {
		return nil, err
	}
	if !toolpublication.ValidID(a.IntentID) || !toolpublication.ValidID(a.ExpectedReviewSHA256) {
		return nil, invalid("invalid_argument", "intent_id and expected_review_sha256 must be lowercase SHA256 identities")
	}
	if err := validateUploadedTaskArgs(&a.planUploadedTaskArgs); err != nil {
		return nil, err
	}
	account, err := m.uploadedTaskAccount(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := m.client.GetHostedTaskSourceIntent(ctx, a.IntentID)
	if err == nil {
		return m.replayUploadedTaskIntent(a, account, existing)
	}
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusNotFound {
		return nil, err
	}
	return m.withUploadedTaskReview(ctx, a.planUploadedTaskArgs, func(review uploadedTaskReview, web, worker string) (any, error) {
		if review.Fingerprint != a.ExpectedReviewSHA256 {
			return nil, invalid("review_changed", "Task source or native app review changed; review again before preparing")
		}
		sources := map[string][]api.HostedTaskSourceEntry{}
		for role, dir := range map[string]string{"web": web, "worker": worker} {
			entries, err := captureTaskSource(dir)
			if err != nil {
				return nil, err
			}
			sources[role] = entries
		}
		raw, _ := json.Marshal(review)
		in := api.HostedTaskSourcePrepare{Review: raw, Sources: sources}
		if _, err := sourcecheck.ValidatePrepare(account, in); err != nil {
			return nil, invalid("source_changed", "Task source no longer matches its reviewed bytes and policy")
		}
		record, err := m.client.PrepareHostedTaskSourceIntent(ctx, a.IntentID, in)
		if err != nil {
			return nil, err
		}
		if tasktoolsource.ValidateRecord(record, account, a.IntentID) != nil || record.ReviewSHA256 != a.ExpectedReviewSHA256 {
			return nil, invalid("invalid_task_source_receipt", "native Task source receipt is invalid; inspect the same intent_id before retrying")
		}
		return record, nil
	})
}
func (m *management) replayUploadedTaskIntent(a prepareUploadedTaskArgs, account string, record api.HostedTaskSourceIntent) (any, error) {
	if tasktoolsource.ValidateRecord(record, account, a.IntentID) != nil || record.ReviewSHA256 != a.ExpectedReviewSHA256 {
		return nil, invalid("intent_conflict", "intent_id belongs to a different retained review")
	}
	review, _ := tasktoolsource.Decode(record.Review)
	if review.WebApp != a.Web.App || review.WorkerApp != a.Worker.App || review.ObserverApp != a.ObserverApp || review.ObserverMetricApp != a.ObserverMetricApp || review.TimeoutSeconds != a.TimeoutSeconds || !reflect.DeepEqual(append([]string{}, review.PreviousWorkerApps...), a.PreviousWorkerApps) {
		return nil, invalid("intent_conflict", "retry must name the original reviewed Task stack")
	}
	for role, source := range map[string]uploadedTaskSource{"web": a.Web, "worker": a.Worker} {
		if source.ExpectedSHA256 != "" && source.ExpectedSHA256 != review.Sources[role] {
			return nil, invalid("intent_conflict", "retry source digest differs from the retained review")
		}
		// Inline retries can be checked without consuming an upload. Uploaded retries
		// refer only to the original immutable review; the old upload may be gone.
		if len(source.Files) > 0 {
			if err := m.checkRetriedTaskSource(source, review.Sources[role]); err != nil {
				return nil, err
			}
		}
	}
	return tasktoolsource.View(record, record.SourceState == "retained", time.Now()), nil
}
func (m *management) checkRetriedTaskSource(source uploadedTaskSource, expected string) error {
	dir, err := m.stageUploadedTaskSource(source)
	if err != nil {
		return err
	}
	defer m.opts.uploads.removeDir(dir)
	entries, err := captureTaskSource(dir)
	if err != nil {
		return err
	}
	digest, err := tasktoolsource.Digest(entries)
	if err != nil || digest != expected {
		return invalid("intent_conflict", "retry source bytes differ from the retained review")
	}
	return nil
}
func captureTaskSource(dir string) ([]api.HostedTaskSourceEntry, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, invalid("invalid_source", "Task source is unavailable")
	}
	defer func() { _ = root.Close() }()
	var entries []api.HostedTaskSourceEntry
	total := 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !entry.IsDir() && !info.Mode().IsRegular() {
			return tasktoolsource.ErrInvalid
		}
		if len(entries) >= api.HostedTaskSourceRoleMaxEntries || info.Size() > int64(api.HostedTaskSourceRoleMaxBytes-total) && !entry.IsDir() {
			return tasktoolsource.ErrInvalid
		}
		item := api.HostedTaskSourceEntry{Path: name, Directory: entry.IsDir(), Mode: uint32(info.Mode().Perm())}
		if !entry.IsDir() {
			file, err := root.Open(name)
			if err != nil {
				return err
			}
			content, err := io.ReadAll(io.LimitReader(file, int64(api.HostedTaskSourceRoleMaxBytes-total)+1))
			closeErr := file.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			item.Content = content
			total += len(content)
			if int64(len(content)) != info.Size() || total > api.HostedTaskSourceRoleMaxBytes {
				return tasktoolsource.ErrInvalid
			}
		}
		entries = append(entries, item)
		return nil
	})
	if err != nil {
		return nil, invalid("source_retention_bounds", "retention requires at most 2 MiB and 1024 normalized regular entries per role")
	}
	if _, err := tasktoolsource.Digest(entries); err != nil {
		return nil, invalid("invalid_source", "retained source requires private normalized paths and modes")
	}
	return entries, nil
}
func uploadedTaskIntentOutputSchema() map[string]any {
	props := map[string]any{}
	for _, name := range []string{"id", "intent_sha256", "review_sha256"} {
		props[name] = map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	}
	for _, name := range []string{"account_id", "requester_key_id"} {
		props[name] = map[string]any{"type": "string", "format": "uuid"}
	}
	for _, name := range []string{"created_at", "expires_at"} {
		props[name] = map[string]any{"type": "string", "format": "date-time"}
	}
	props["version"] = map[string]any{"type": "integer", "const": 1}
	props["source_state"] = map[string]any{"type": "string", "enum": []string{"retained", "expired"}}
	props["review"] = uploadedTaskReviewOutputSchema()
	props["ready_to_apply"] = map[string]any{"type": "boolean", "const": false}
	props["bindings_verified"] = map[string]any{"type": "boolean", "const": false}
	props["owner_authorization_required"] = map[string]any{"type": "boolean", "const": true}
	required := []string{}
	for name := range props {
		required = append(required, name)
	}
	slices.Sort(required)
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
}
