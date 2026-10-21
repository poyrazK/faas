// ADR-1656: only fresh authenticated pushes can admit profile-owned execution.
package ci

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func automationFixture(t *testing.T) (*jobsHarness, ExecutionProfile, Config) {
	t.Helper()
	h := newJobsHarness(t)
	profile := h.profile(t, "automation")
	cfg := h.backend.Config
	cfg.AutomationEnabled, cfg.WorkerEnabled, cfg.TransportEnabled = true, true, true
	setup := cfg
	setup.AutomationEnabled, setup.DispatchEnabled, setup.WorkerEnabled, setup.TransportEnabled = false, false, false, false
	if _, err := SaveAutomationPolicy(t.Context(), setup, h.store, h.verify, h.account, h.binding.ID, AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	return h, profile, cfg
}

func automationDelivery(h *jobsHarness, sha, branch string) []byte {
	raw, _ := json.Marshal(map[string]any{"ref": "refs/heads/" + branch, "after": sha,
		"installation": map[string]any{"id": h.binding.InstallationID}, "repository": map[string]any{"id": h.binding.RepositoryID, "full_name": h.binding.Repository}})
	return raw
}

func captureAutomationTest(t *testing.T, h *jobsHarness, cfg Config, sha string) {
	t.Helper()
	if err := RecordVerifiedGitHubDelivery(t.Context(), cfg, h.store, uuid.NewString(), "push", automationDelivery(h, sha, "main")); err != nil {
		t.Fatal(err)
	}
}

func TestCIAutomationFreshPushDeduplicatesWithoutPromotingBlockedReceipts(t *testing.T) {
	h, profile, cfg := automationFixture(t)
	off := cfg
	off.AutomationEnabled = false
	captureAutomationTest(t, h, off, strings.Repeat("a", 40))
	captureAutomationTest(t, h, cfg, strings.Repeat("a", 40))
	if h.count(t, "SELECT count(*) FROM ci_automation_events") != 0 || h.count(t, "SELECT count(*) FROM ci_github_triggers") != 1 {
		t.Fatal("enabling automation promoted an existing blocked trigger")
	}
	if worked, err := AdmitGitHubOnce(t.Context(), cfg, h.store, h.verify); err != nil || !worked {
		t.Fatal("existing metadata admission changed")
	}
	if h.count(t, "SELECT count(*) FROM ci_runs WHERE status='blocked'") != 1 || h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
		t.Fatal("old receipt became compute work")
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 6)
	for range 6 {
		wg.Go(func() {
			errorsCh <- RecordVerifiedGitHubDelivery(t.Context(), cfg, h.store, uuid.NewString(), "push", automationDelivery(h, strings.Repeat("b", 40), "main"))
		})
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if h.count(t, "SELECT count(*) FROM ci_automation_events") != 1 || h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
		t.Fatal("capture duplicated a delivery or admitted execution inside githubd")
	}
	if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err != nil || !worked {
		t.Fatalf("automatic admission: worked=%v err=%v", worked, err)
	}
	events, err := h.store.AutomationEvents(t.Context(), h.account, h.binding.ID)
	if err != nil || len(events) != 1 || events[0].Status != "admitted" || events[0].ExecutionID == "" {
		t.Fatal("execution and event acknowledgement were not linked")
	}
	row, err := h.store.Execution(t.Context(), h.account, events[0].ExecutionID)
	if err != nil || row.ProfileID != profile.ID || row.Automation == nil || row.Automation.EventID != events[0].ID || row.CommitSHA != strings.Repeat("b", 40) {
		t.Fatal("execution lost frozen profile or source provenance")
	}
	h.restart() // Discard the caller's response, then recover from durable state.
	if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err != nil || worked {
		t.Fatal("restart repeated an acknowledged event")
	}
	captureAutomationTest(t, h, off, strings.Repeat("b", 40))
	if h.count(t, "SELECT count(*) FROM ci_github_triggers") != 1 || h.count(t, "SELECT count(*) FROM ci_executions") != 1 || h.count(t, "SELECT count(*) FROM job_runs") != 0 {
		t.Fatal("mode changes duplicated intent or bypassed the execution worker")
	}
	manual := JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: SubmitRequest{CommitSHA: row.CommitSHA}}
	if _, _, err := AdmitJobs(t.Context(), cfg, h.store, h.verify, h.account, "github-auto:"+events[0].ID, manual); !errors.Is(err, ErrConflict) {
		t.Fatal("manual request reused automation identity")
	}
	if _, err := h.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", h.account); err != nil {
		t.Fatalf("account deletion could not cascade automatic intent: %v", err)
	}
	if h.count(t, "SELECT count(*) FROM ci_automation_events") != 0 || h.count(t, "SELECT count(*) FROM ci_automation_policies") != 0 {
		t.Fatal("account deletion retained automation authority")
	}
}

func TestCIAutomationRevocationAndPolicyChangesDoNotReplay(t *testing.T) {
	for _, name := range []string{"policy", "profile", "binding", "generation", "image", "access"} {
		t.Run(name, func(t *testing.T) {
			h, profile, cfg := automationFixture(t)
			captureAutomationTest(t, h, cfg, strings.Repeat("b", 40))
			verify := func(ctx context.Context, _ string, _, _ int64, _ string) error {
				switch name {
				case "policy":
					_, err := h.store.DisableAutomationPolicy(ctx, h.account, h.binding.ID)
					return err
				case "profile":
					_, err := h.store.DisableExecutionProfile(ctx, h.account, profile.ID)
					return err
				case "binding":
					return h.store.DisableGitHubBinding(ctx, h.account, h.binding.Name)
				case "generation":
					req := h.binding.GitHubBindingRequest
					req.PushBranches = []string{"main", "release"}
					_, err := h.store.PutGitHubBinding(ctx, h.account, h.binding.Name, req)
					return err
				case "image":
					_, err := h.pool.Exec(ctx, "UPDATE jobs SET image_storage_key='replacement/image.ext4' WHERE id=$1", h.req.Templates["test"])
					return err
				case "access":
					return ErrGitHubAccess
				}
				return nil
			}
			if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, verify); err != nil || !worked {
				t.Fatalf("revoke: worked=%v err=%v", worked, err)
			}
			events, err := h.store.AutomationEvents(t.Context(), h.account, h.binding.ID)
			if err != nil || len(events) != 1 || events[0].Status != "ignored" || h.count(t, "SELECT count(*) FROM ci_executions") != 0 {
				t.Fatal("revoked authority admitted execution or retained a live claim")
			}
		})
	}
	h, profile, cfg := automationFixture(t)
	captureAutomationTest(t, h, cfg, strings.Repeat("a", 40))
	if _, err := h.store.DisableAutomationPolicy(t.Context(), h.account, h.binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, AutomationRequest{ProfileID: profile.ID, PushBranches: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	captureAutomationTest(t, h, cfg, strings.Repeat("a", 40))
	if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err != nil || worked {
		t.Fatal("re-enabling policy replayed ignored source evidence")
	}
}

func TestCIAutomationStaleLeasesAndAtomicAdmission(t *testing.T) {
	h, profile, cfg := automationFixture(t)
	captureAutomationTest(t, h, cfg, strings.Repeat("b", 40))
	old, err := h.store.ClaimAutomationEvent(t.Context(), h.account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", old.ID); err != nil {
		t.Fatal(err)
	}
	h.restart()
	current, err := h.store.ClaimAutomationEvent(t.Context(), h.account)
	if err != nil || current.LeaseToken == old.LeaseToken {
		t.Fatal("expired claim did not recover with a new token")
	}
	req := JobsSubmitRequest{ProfileID: profile.ID, SubmitRequest: SubmitRequest{CommitSHA: old.Source.CommitSHA}, automation: &old}
	if _, _, err := AdmitJobs(t.Context(), cfg, h.store, h.verify, h.account, "github-auto:"+old.ID, req); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("stale claim admitted execution")
	}
	if err := h.store.FailAutomationEvent(t.Context(), old, "unavailable", false); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("stale failure replaced a newer lease")
	}
	// Expire the source lease after execution insertion but before acknowledgement.
	// Both changes must roll back as a single admission transaction.
	if _, err := h.pool.Exec(t.Context(), `CREATE FUNCTION expire_ci_auto() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 UPDATE ci_automation_events SET lease_until=clock_timestamp()-interval '1 second' WHERE id=(NEW.snapshot->'automation'->>'event_id')::uuid;
 RETURN NEW; END $$;
 CREATE TRIGGER expire_ci_auto AFTER INSERT ON ci_executions FOR EACH ROW EXECUTE FUNCTION expire_ci_auto()`); err != nil {
		t.Fatal(err)
	}
	req.automation = &current
	if _, _, err := AdmitJobs(t.Context(), cfg, h.store, h.verify, h.account, "github-auto:"+current.ID, req); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired acknowledgement: %v", err)
	}
	if h.count(t, "SELECT count(*) FROM ci_executions") != 0 || h.count(t, "SELECT count(*) FROM ci_automation_events WHERE execution_id IS NOT NULL") != 0 {
		t.Fatal("partial automatic admission committed")
	}
	if _, err := h.pool.Exec(t.Context(), "DROP TRIGGER expire_ci_auto ON ci_executions"); err != nil {
		t.Fatal(err)
	}
	if _, replay, err := AdmitJobs(t.Context(), cfg, h.store, h.verify, h.account, "github-auto:"+current.ID, req); err != nil || replay {
		t.Fatalf("recovery after rollback: %v", err)
	}
}

func TestCIAutomationGatesBranchesAndSharedCaptureQuota(t *testing.T) {
	h, profile, cfg := automationFixture(t)
	for _, branches := range [][]string{nil, {"*"}, {"main", "main"}, {"unlisted"}} {
		if _, err := SaveAutomationPolicy(t.Context(), cfg, h.store, h.verify, h.account, h.binding.ID, AutomationRequest{ProfileID: profile.ID, PushBranches: branches}); err == nil {
			t.Fatal("implicit, duplicate or unauthorized branches were accepted")
		}
	}
	captureAutomationTest(t, h, cfg, strings.Repeat("b", 40))
	for _, name := range []string{"automation", "worker", "transport", "dispatch", "github", "internal"} {
		disabled := cfg
		switch name {
		case "automation":
			disabled.AutomationEnabled = false
		case "worker":
			disabled.WorkerEnabled = false
		case "transport":
			disabled.TransportEnabled = false
		case "dispatch":
			disabled.DispatchEnabled = false
		case "github":
			disabled.GitHubEnabled = false
		case "internal":
			disabled.Enabled = false
		}
		if worked, err := AdmitAutomationOnce(t.Context(), disabled, h.store, h.verify); err != nil || worked {
			t.Fatalf("automation escaped %s gate", name)
		}
	}
	if h.count(t, "SELECT count(*) FROM ci_automation_events WHERE attempts<>0") != 0 {
		t.Fatal("flag-off worker consumed pending source")
	}
	if _, err := h.pool.Exec(t.Context(), `INSERT INTO ci_github_triggers(id,account_id,binding_id,event_key,snapshot)
 SELECT gen_random_uuid(),$1,$2,lpad(to_hex(n),64,'0'),'{}'::jsonb FROM generate_series(1,$3::integer) n`, h.account, h.binding.ID, api.CIGitHubMaxTriggersPerAccount-1); err != nil {
		t.Fatal(err)
	}
	if err := RecordVerifiedGitHubDelivery(t.Context(), cfg, h.store, uuid.NewString(), "push", automationDelivery(h, strings.Repeat("c", 40), "main")); !errors.Is(err, ErrLimit) {
		t.Fatal("automation received a second independent trigger quota")
	}
	captureAutomationTest(t, h, cfg, strings.Repeat("b", 40)) // Replay still works at the ceiling.
	if h.count(t, "SELECT count(*) FROM ci_automation_events") != 1 {
		t.Fatal("quota failure retained partial source evidence")
	}
}

func TestCIAutomationRetriesProviderAndCapacityWithinBounds(t *testing.T) {
	h, _, cfg := automationFixture(t)
	captureAutomationTest(t, h, cfg, strings.Repeat("b", 40))
	verify := func(context.Context, string, int64, int64, string) error { return ErrGitHubUnavailable }
	for range api.CIGitHubMaxAttempts {
		if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, verify); !worked || !errors.Is(err, ErrGitHubUnavailable) {
			t.Fatal("provider failure did not retain a retry")
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET next_attempt_at=clock_timestamp()"); err != nil {
			t.Fatal(err)
		}
	}
	events, err := h.store.AutomationEvents(t.Context(), h.account, h.binding.ID)
	if err != nil || len(events) != 1 || events[0].Status != "dead" || events[0].Attempts != api.CIGitHubMaxAttempts || events[0].LastError != "unavailable" {
		t.Fatal("provider retry budget was unbounded or lost its classification")
	}
	if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err != nil || worked {
		t.Fatal("dead source automatically replayed")
	}
	var active []Execution
	for _, key := range []string{"capacity-a", "capacity-b", "capacity-c", "capacity-d"} {
		active = append(active, h.admit(t, key))
	}
	captureAutomationTest(t, h, cfg, strings.Repeat("c", 40))
	if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); !worked || !errors.Is(err, ErrLimit) {
		t.Fatal("automation escaped ordinary active-execution limits")
	}
	for _, row := range active {
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
		h.tick(t, row)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE ci_automation_events SET next_attempt_at=clock_timestamp()"); err != nil {
		t.Fatal(err)
	}
	if worked, err := AdmitAutomationOnce(t.Context(), cfg, h.store, h.verify); err != nil || !worked {
		t.Fatal("capacity retry did not recover after existing executions drained")
	}
}
