package ciapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
)

func newSourceCheckAPI(t *testing.T, checks bool) (*automationRecoveryAPI, string) {
	t.Helper()
	f := newAutomationRecoveryAPI(t)
	store := ci.NewPostgresStore(f.pool)
	profiles, err := store.ExecutionProfiles(t.Context(), f.account)
	if err != nil || len(profiles) != 1 {
		t.Fatal("profile missing")
	}
	if w := f.call(t, ciapi.Handler.PutAutomationPolicy, f.account, f.binding, "", "", ci.AutomationRequest{ProfileID: profiles[0].ID, PushBranches: []string{"main"}, IncludePaths: []string{"src/**"}}); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
	f.handler.Config.GitHubChecksEnabled, f.handler.Config.ExecutionChecksEnabled = checks, checks
	cfg := f.handler.Config
	cfg.DispatchEnabled, cfg.WorkerEnabled, cfg.TransportEnabled = true, true, true
	base, head := strings.Repeat("a", 40), strings.Repeat("c", 40)
	raw, _ := json.Marshal(map[string]any{"ref": "refs/heads/main", "before": base, "after": head, "installation": map[string]any{"id": 12}, "repository": map[string]any{"id": 34, "full_name": "private/automation-recovery"}})
	if err := ci.RecordVerifiedGitHubDelivery(t.Context(), cfg, store, uuid.NewString(), "push", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := ci.AdmitAutomationWithResolvers(t.Context(), cfg, store, f.handler.VerifyGitHub, ci.AutomationResolvers{Changes: func(context.Context, ci.GitHubBinding, string, string) (ci.ChangedFiles, error) {
		return ci.ChangedFiles{BaseSHA: base, HeadSHA: head, MergeBaseSHA: base, Complete: true, Files: []ci.ChangedFile{{Path: "docs/private-filename", Status: "modified"}}}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	var event string
	if err := f.pool.QueryRow(t.Context(), "SELECT id FROM ci_automation_events WHERE status='ignored' AND last_error='paths_filtered'").Scan(&event); err != nil {
		t.Fatal(err)
	}
	return f, event
}

func TestCISourceCheckAPIValidationScopeAndIdempotency(t *testing.T) {
	f, event := newSourceCheckAPI(t, true)
	for _, action := range []ciapi.Action{ciapi.Handler.ListSourceCheckDeliveries, ciapi.Handler.SourceCheckDelivery, ciapi.Handler.RetrySourceCheckDelivery} {
		if w := f.call(t, action, f.account, "../outside", event, "invalid", ci.CheckRetryRequest{}); w.Code != http.StatusNotFound {
			t.Fatalf("malformed binding selector: %d %s", w.Code, w.Body)
		}
	}
	for _, scope := range []struct{ account, binding, event string }{{f.other, f.binding, event}, {f.account, uuid.NewString(), event}, {f.account, f.binding, f.eventID}} {
		for _, action := range []ciapi.Action{ciapi.Handler.SourceCheckDelivery, ciapi.Handler.RetrySourceCheckDelivery} {
			if w := f.call(t, action, scope.account, scope.binding, scope.event, "foreign", ci.CheckRetryRequest{}); w.Code != http.StatusNotFound {
				t.Fatalf("scope escaped: %d %s", w.Code, w.Body)
			}
		}
	}
	for _, body := range []any{nil, map[string]any{}, map[string]any{"expected_retry_generation": nil}, map[string]any{"expected_retry_generation": -1}, map[string]any{"expected_retry_generation": 4}, map[string]any{"expected_retry_generation": 0, "commit_sha": strings.Repeat("d", 40)}} {
		if w := f.call(t, ciapi.Handler.RetrySourceCheckDelivery, f.account, f.binding, event, "invalid", body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid body: %d %s", w.Code, w.Body)
		}
	}
	if w := f.call(t, ciapi.Handler.RetrySourceCheckDelivery, f.account, f.binding, event, "", ci.CheckRetryRequest{}); w.Code != http.StatusBadRequest {
		t.Fatal("missing key accepted")
	}
	if w := f.call(t, ciapi.Handler.RetrySourceCheckDelivery, f.account, f.binding, event, "too-soon", ci.CheckRetryRequest{}); w.Code != http.StatusConflict {
		t.Fatal("pending delivery retried")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE ci_automation_path_checks SET status='dead',attempts=$2,last_error='unavailable' WHERE event_id=$1", event, api.CIGitHubMaxAttempts); err != nil {
		t.Fatal(err)
	}
	audits := 0
	f.handler.Audit = func(_ context.Context, kind string, owner *string, data map[string]any) {
		if kind != "ci.source_check.retry_requested" || owner == nil || *owner != f.account || data["binding_id"] != f.binding || data["event_id"] != event || len(data) != 4 {
			t.Error("audit lost bounded source identity")
		}
		audits++
	}
	first := f.call(t, ciapi.Handler.RetrySourceCheckDelivery, f.account, f.binding, event, "lost-response", ci.CheckRetryRequest{})
	f.handler.Store = ci.NewPostgresStore(f.pool)
	again := f.call(t, ciapi.Handler.RetrySourceCheckDelivery, f.account, f.binding, event, "lost-response", ci.CheckRetryRequest{})
	if first.Code != http.StatusAccepted || again.Code != http.StatusAccepted || again.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), again.Body.Bytes()) || audits != 1 {
		t.Fatalf("receipt replay: %d %s / %d %s", first.Code, first.Body, again.Code, again.Body)
	}
	var delivery ci.SourceCheckDelivery
	w := f.call(t, ciapi.Handler.SourceCheckDelivery, f.account, f.binding, event, "", nil)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &delivery) != nil || !delivery.RetryPending || delivery.Retryable || delivery.RetryGeneration != 0 || delivery.LastRetry == nil {
		t.Fatalf("pending intent: %s", w.Body)
	}
	if w := f.call(t, ciapi.Handler.RetrySourceCheckDelivery, f.account, f.binding, event, "another-key", ci.CheckRetryRequest{}); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "ci_check_retry_conflict") {
		t.Fatal("pending intent admitted again")
	}
	if f.handler.Config.WorkerEnabled || f.handler.Config.DispatchEnabled || f.handler.Config.TransportEnabled {
		t.Fatal("feedback recovery enabled compute")
	}
	f.handler.Config.GitHubChecksEnabled, f.handler.Config.ExecutionChecksEnabled = false, false
	w = f.call(t, ciapi.Handler.AutomationEventStatus, f.account, f.binding, event, "", nil)
	var source ci.AutomationEventSummary
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &source) != nil || source.Retryable || source.CheckDelivery == nil || !source.CheckDelivery.RetryPending || source.PathDecision == nil || source.PathDecision.Outcome != "skip" {
		t.Fatalf("source diagnostics: %s", w.Body)
	}
	for _, private := range []string{"snapshot", "key_digest", "lease_token", "installation_id", "private-filename", "command"} {
		if strings.Contains(w.Body.String(), private) || strings.Contains(first.Body.String(), private) {
			t.Fatalf("exposed %s", private)
		}
	}
	if w := f.call(t, ciapi.Handler.ListSourceCheckDeliveries, f.account, f.binding, "", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), event) {
		t.Fatal("bounded source list unavailable")
	}
	if w := f.call(t, ciapi.Handler.RetrySourceCheckDelivery, f.account, f.binding, event, "lost-response", ci.CheckRetryRequest{}); w.Code != http.StatusNotFound {
		t.Fatal("disabled publication admitted replay")
	}
}

func TestCISourceCheckAPIUncapturedAndGates(t *testing.T) {
	f, event := newSourceCheckAPI(t, false)
	w := f.call(t, ciapi.Handler.SourceCheckDelivery, f.account, f.binding, event, "", nil)
	var delivery ci.SourceCheckDelivery
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &delivery) != nil || delivery.DeliveryStatus != "not_requested" || delivery.Retryable {
		t.Fatalf("uncaptured status: %s", w.Body)
	}
	f.handler.Config.GitHubChecksEnabled, f.handler.Config.ExecutionChecksEnabled = true, true
	f.handler.VerifyGitHub = func(context.Context, string, int64, int64, string) error {
		t.Error("uncaptured source called provider")
		return nil
	}
	if w := f.call(t, ciapi.Handler.RetrySourceCheckDelivery, f.account, f.binding, event, "no-new-check", ci.CheckRetryRequest{}); w.Code != http.StatusConflict {
		t.Fatal("uncaptured source manufactured feedback")
	}
	for _, gate := range []string{"internal", "github", "checks", "execution_checks"} {
		cfg := f.handler.Config
		switch gate {
		case "internal":
			f.handler.Config.Enabled = false
		case "github":
			f.handler.Config.GitHubEnabled = false
		case "checks":
			f.handler.Config.GitHubChecksEnabled = false
		case "execution_checks":
			f.handler.Config.ExecutionChecksEnabled = false
		}
		if w := f.call(t, ciapi.Handler.RetrySourceCheckDelivery, f.account, f.binding, event, "off", ci.CheckRetryRequest{}); w.Code != http.StatusNotFound {
			t.Fatalf("gate %s: %d", gate, w.Code)
		}
		f.handler.Config = cfg
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM ci_source_check_retries").Scan(&count); err != nil || count != 0 {
		t.Fatal("disabled/missing feedback wrote retry")
	}
}
