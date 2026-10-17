package ci

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type gitHubTestStore interface {
	Store
	GitHubStore
}
type gitHubHarness struct {
	store          gitHubTestStore
	account, other string
	restart        func() gitHubTestStore
	advance        func()
}

func githubConfig(account string, checks bool) Config {
	return ConfigFromEnv(func(key string) string {
		switch key {
		case InternalEnv, GitHubEnv:
			return "1"
		case AccountsEnv:
			return account
		case GitHubChecksEnv:
			if checks {
				return "1"
			}
		}
		return ""
	})
}

func sourceEvent() GitHubEvent {
	return GitHubEvent{Kind: "push", InstallationID: 41, RepositoryID: 123, Repository: "gregale/ci-qualification", CommitSHA: strings.Repeat("a", 40), Branch: "main"}
}

func (h *gitHubHarness) bind(t *testing.T) (Revision, GitHubBinding) {
	t.Helper()
	r, err := h.store.PutRevision(t.Context(), h.account, "checks", executionDefinition())
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.store.PutGitHubBinding(t.Context(), h.account, "checks", GitHubBindingRequest{RevisionID: r.ID, InstallationID: 41, RepositoryID: 123, PushBranches: []string{"main"}, PullRequestBranches: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	return r, b
}

func TestGitHubSourcesMemory(t *testing.T) {
	exerciseGitHubSources(t, func(t *testing.T) *gitHubHarness {
		clock := &qualificationClock{}
		clock.nanos.Store(time.Now().UTC().UnixNano())
		s := NewMemoryStore()
		s.executionClock = clock.now
		return &gitHubHarness{store: s, account: uuid.NewString(), other: uuid.NewString(), restart: func() gitHubTestStore { return s }, advance: func() { clock.advance(api.CIGitHubMaxBackoffSeconds + api.CIGitHubLeaseSeconds + 1) }}
	})
}

func TestGitHubSourcesPostgres(t *testing.T) {
	pool := qualificationDatabase(t)
	exerciseGitHubSources(t, func(t *testing.T) *gitHubHarness {
		account, other := uuid.NewString(), uuid.NewString()
		if _, err := pool.Exec(t.Context(), "INSERT INTO accounts(id) VALUES ($1),($2)", account, other); err != nil {
			t.Fatal(err)
		}
		return &gitHubHarness{store: NewPostgresStore(pool), account: account, other: other, restart: func() gitHubTestStore { return NewPostgresStore(pool) }, advance: func() {
			for _, table := range []string{"ci_github_triggers", "ci_github_checks"} {
				// Fixed test-owned table names, never customer-controlled SQL.
				query := "UPDATE " + table + " SET next_attempt_at=clock_timestamp()-interval '1 second', lease_until=CASE WHEN lease_until IS NULL THEN NULL ELSE clock_timestamp()-interval '1 second' END WHERE account_id=$1"
				if _, err := pool.Exec(t.Context(), query, account); err != nil {
					t.Fatal(err)
				}
			}
		}}
	})
	var account string
	if err := pool.QueryRow(t.Context(), "SELECT account_id::text FROM ci_github_bindings LIMIT 1").Scan(&account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM ci_github_bindings WHERE account_id=$1)+(SELECT count(*) FROM ci_github_triggers WHERE account_id=$1)+(SELECT count(*) FROM ci_github_checks WHERE account_id=$1)", account).Scan(&count); err != nil || count != 0 {
		t.Fatalf("account deletion leaked source evidence: %d %v", count, err)
	}
}

func exerciseGitHubSources(t *testing.T, factory func(*testing.T) *gitHubHarness) {
	t.Helper()
	t.Run("one-event-one-blocked-receipt-with-pinned-revision", func(t *testing.T) {
		h := factory(t)
		revision, binding := h.bind(t)
		again, err := h.store.PutGitHubBinding(t.Context(), h.account, "checks", binding.GitHubBindingRequest)
		if err != nil || again.Generation != binding.Generation || again.ID != binding.ID {
			t.Fatal("idempotent bind changed generation")
		}
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), sourceEvent()); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		rows, err := h.store.ListRuns(t.Context(), h.account)
		if err != nil || len(rows) != 0 {
			t.Fatal("githubd inbox created customer intent")
		}
		claim, err := h.store.ClaimGitHubTrigger(t.Context(), h.account)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.ClaimGitHubTrigger(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatalf("concurrent trigger claim accepted: %v", err)
		}
		changed := executionDefinition()
		job := changed.Jobs["test"]
		job.Steps[0].Run = "changed after webhook"
		changed.Jobs["test"] = job
		newRevision, err := h.store.PutRevision(t.Context(), h.account, "checks", changed)
		if err != nil {
			t.Fatal(err)
		}
		req := binding.GitHubBindingRequest
		req.RevisionID = newRevision.ID
		if _, err := h.store.PutGitHubBinding(t.Context(), h.account, "checks", req); err != nil {
			t.Fatal(err)
		}
		run, err := h.store.AdmitGitHubTrigger(t.Context(), claim)
		if err != nil || run.Status != StatusBlocked || run.BlockedReason != ComputeUnavailable || run.RevisionID != revision.ID || run.CommitSHA != sourceEvent().CommitSHA || run.Plan.DefinitionDigest != revision.Digest || run.GitHub == nil || run.GitHub.Generation != binding.Generation {
			t.Fatalf("trigger lost immutable source intent: %+v %v", run, err)
		}
		replayed, err := h.store.AdmitGitHubTrigger(t.Context(), claim)
		if err != nil || replayed.ID != run.ID {
			t.Fatal("admission retry duplicated a run")
		}
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), sourceEvent()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.ClaimGitHubTrigger(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatal("new delivery ID replayed a semantic event")
		}
		h.store = h.restart()
		recovered, err := h.store.Run(t.Context(), h.account, run.ID)
		if err != nil || recovered.GitHub == nil || recovered.GitHub.DeliveryID != run.GitHub.DeliveryID {
			t.Fatal("restart lost source provenance")
		}
		events, err := h.store.Events(t.Context(), h.account, run.ID)
		if err != nil || len(events) != 1 || events[0].Type != StatusBlocked {
			t.Fatal("trigger history not atomic")
		}
		check, err := h.store.ClaimGitHubCheck(t.Context(), h.account)
		if err != nil || check.RunID != run.ID || check.Source.BindingID != binding.ID {
			t.Fatalf("missing atomic check intent: %+v %v", check, err)
		}
		if _, err := h.store.ClaimGitHubCheck(t.Context(), h.other); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatal("cross-account check leaked")
		}
		if err := h.store.FinishGitHubCheck(t.Context(), check, 42, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.ClaimGitHubCheck(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatal("sent check was retried")
		}
	})
	t.Run("branch-installation-repository-and-account-isolation", func(t *testing.T) {
		h := factory(t)
		revision, _ := h.bind(t)
		if _, err := h.store.PutGitHubBinding(t.Context(), h.other, "foreign", GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 41, RepositoryID: 123, PushBranches: []string{"main"}}); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign revision accepted")
		}
		if err := h.store.DisableGitHubBinding(t.Context(), h.other, "checks"); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign binding disabled")
		}
		for _, mutate := range []func(*GitHubEvent){func(e *GitHubEvent) { e.Branch = "dev" }, func(e *GitHubEvent) { e.RepositoryID++ }, func(e *GitHubEvent) { e.InstallationID++ }, func(e *GitHubEvent) { e.Repository = "other/repo" }} {
			event := sourceEvent()
			mutate(&event)
			if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), event); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.store.RecordGitHubDelivery(t.Context(), h.other, uuid.NewString(), sourceEvent()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.ClaimGitHubTrigger(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatal("unmatched event admitted")
		}
		if rows, err := h.store.GitHubBindings(t.Context(), h.other); err != nil || len(rows) != 0 {
			t.Fatal("foreign bindings leaked")
		}
	})
	t.Run("expired-trigger-and-check-leases-fence-old-workers", func(t *testing.T) {
		h := factory(t)
		h.bind(t)
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), sourceEvent()); err != nil {
			t.Fatal(err)
		}
		old, err := h.store.ClaimGitHubTrigger(t.Context(), h.account)
		if err != nil {
			t.Fatal(err)
		}
		h.advance()
		h.store = h.restart()
		if _, err := h.store.AdmitGitHubTrigger(t.Context(), old); !errors.Is(err, ErrLeaseLost) {
			t.Fatal("expired lease admitted a run")
		}
		current, err := h.store.ClaimGitHubTrigger(t.Context(), h.account)
		if err != nil || current.ID != old.ID || current.LeaseToken == old.LeaseToken {
			t.Fatal("trigger recovery changed source identity")
		}
		if err := h.store.FailGitHubTrigger(t.Context(), old, "unavailable"); !errors.Is(err, ErrLeaseLost) {
			t.Fatal("stale worker overwrote inbox")
		}
		if _, err := h.store.AdmitGitHubTrigger(t.Context(), current); err != nil {
			t.Fatal(err)
		}
		first, err := h.store.ClaimGitHubCheck(t.Context(), h.account)
		if err != nil {
			t.Fatal(err)
		}
		h.advance()
		second, err := h.store.ClaimGitHubCheck(t.Context(), h.account)
		if err != nil || second.RunID != first.RunID || second.LeaseToken == first.LeaseToken {
			t.Fatal("check recovery changed its identity")
		}
		if err := h.store.FinishGitHubCheck(t.Context(), first, 1, ""); !errors.Is(err, ErrLeaseLost) {
			t.Fatal("stale check acknowledgement accepted")
		}
		if err := h.store.FinishGitHubCheck(t.Context(), second, 1, ""); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("flags-off-and-check-publication-separate", func(t *testing.T) {
		h := factory(t)
		h.bind(t)
		event := sourceEvent()
		if err := RecordVerifiedGitHubDelivery(t.Context(), Config{}, h.store, uuid.NewString(), "push", []byte(`invalid`)); err != nil {
			t.Fatal("disabled ingress parsed a payload")
		}
		if _, err := h.store.ClaimGitHubTrigger(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatal("disabled ingress created work")
		}
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), event); err != nil {
			t.Fatal(err)
		}
		verifyCalls, publishCalls := 0, 0
		verify := func(context.Context, string, int64, int64, string) error { verifyCalls++; return nil }
		publish := func(context.Context, GitHubCheck) (int64, error) { publishCalls++; return 7, nil }
		cfg := githubConfig(h.account, false)
		if worked, err := AdmitGitHubOnce(t.Context(), cfg, h.store, verify); err != nil || !worked || verifyCalls != 1 {
			t.Fatalf("admission worker: %v", err)
		}
		if worked, err := PublishGitHubCheckOnce(t.Context(), cfg, h.store, verify, publish); err != nil || worked || publishCalls != 0 || verifyCalls != 1 {
			t.Fatal("checks gate performed outbound work")
		}
		cfg.GitHubChecksEnabled = true
		if worked, err := PublishGitHubCheckOnce(t.Context(), cfg, h.store, verify, publish); err != nil || !worked || publishCalls != 1 {
			t.Fatalf("checks publication: %v", err)
		}
	})
	t.Run("unavailable-permissions-retry-without-duplicate-intent", func(t *testing.T) {
		h := factory(t)
		h.bind(t)
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), sourceEvent()); err != nil {
			t.Fatal(err)
		}
		cfg := githubConfig(h.account, false)
		if _, err := AdmitGitHubOnce(t.Context(), cfg, h.store, func(context.Context, string, int64, int64, string) error { return ErrGitHubUnavailable }); !errors.Is(err, ErrGitHubUnavailable) {
			t.Fatal("upstream failure lost retry")
		}
		if rows, _ := h.store.ListRuns(t.Context(), h.account); len(rows) != 0 {
			t.Fatal("unverified source created a run")
		}
		h.advance()
		if worked, err := AdmitGitHubOnce(t.Context(), cfg, h.store, func(context.Context, string, int64, int64, string) error { return nil }); err != nil || !worked {
			t.Fatal("source retry did not recover")
		}
		if rows, _ := h.store.ListRuns(t.Context(), h.account); len(rows) != 1 {
			t.Fatal("source retry duplicated a run")
		}
	})
	t.Run("revoked-access-and-disabled-bindings-do-not-admit", func(t *testing.T) {
		h := factory(t)
		h.bind(t)
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), sourceEvent()); err != nil {
			t.Fatal(err)
		}
		if _, err := AdmitGitHubOnce(t.Context(), githubConfig(h.account, false), h.store, func(context.Context, string, int64, int64, string) error { return ErrGitHubAccess }); err != nil {
			t.Fatal(err)
		}
		h.advance()
		if _, err := h.store.ClaimGitHubTrigger(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatal("revoked access retried")
		}
		event := sourceEvent()
		event.CommitSHA = strings.Repeat("b", 40)
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), event); err != nil {
			t.Fatal(err)
		}
		claim, err := h.store.ClaimGitHubTrigger(t.Context(), h.account)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.store.DisableGitHubBinding(t.Context(), h.account, "checks"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.AdmitGitHubTrigger(t.Context(), claim); !errors.Is(err, ErrLeaseLost) {
			t.Fatal("disable did not fence pending admission")
		}
		if rows, _ := h.store.ListRuns(t.Context(), h.account); len(rows) != 0 {
			t.Fatal("revoked source created intent")
		}
	})
	t.Run("bounded-check-retries-stop", func(t *testing.T) {
		h := factory(t)
		h.bind(t)
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), sourceEvent()); err != nil {
			t.Fatal(err)
		}
		claim, err := h.store.ClaimGitHubTrigger(t.Context(), h.account)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.AdmitGitHubTrigger(t.Context(), claim); err != nil {
			t.Fatal(err)
		}
		for i := range api.CIGitHubMaxAttempts {
			check, err := h.store.ClaimGitHubCheck(t.Context(), h.account)
			if err != nil || check.Attempts != i+1 {
				t.Fatalf("retry attempt %d: %v", i, err)
			}
			if err := h.store.FinishGitHubCheck(t.Context(), check, 0, "publish_failed"); err != nil {
				t.Fatal(err)
			}
			h.advance()
		}
		if _, err := h.store.ClaimGitHubCheck(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatal("dead check retried indefinitely")
		}
	})
	t.Run("disable-fences-in-flight-check-publication", func(t *testing.T) {
		h := factory(t)
		h.bind(t)
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), sourceEvent()); err != nil {
			t.Fatal(err)
		}
		trigger, err := h.store.ClaimGitHubTrigger(t.Context(), h.account)
		if err != nil {
			t.Fatal(err)
		}
		run, err := h.store.AdmitGitHubTrigger(t.Context(), trigger)
		if err != nil {
			t.Fatal(err)
		}
		check, err := h.store.ClaimGitHubCheck(t.Context(), h.account)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.store.DisableGitHubBinding(t.Context(), h.account, "checks"); err != nil {
			t.Fatal(err)
		}
		if err := h.store.FinishGitHubCheck(t.Context(), check, 7, ""); !errors.Is(err, ErrLeaseLost) {
			t.Fatal("disabled binding accepted in-flight Check acknowledgement")
		}
		if _, err := h.store.ClaimGitHubCheck(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatal("disabled binding published another Check")
		}
		if row, err := h.store.Run(t.Context(), h.account, run.ID); err != nil || row.Status != StatusBlocked {
			t.Fatal("disable rewrote retained run intent")
		}
	})
	t.Run("run-cap-rejects-atomically", func(t *testing.T) {
		h := factory(t)
		revision, _ := h.bind(t)
		for i := range api.CIMaxRunsPerAccount {
			if _, _, err := h.store.Submit(t.Context(), h.account, fmt.Sprintf("manual-%d", i), SubmitRequest{RevisionID: revision.ID, CommitSHA: sourceEvent().CommitSHA}); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), sourceEvent()); err != nil {
			t.Fatal(err)
		}
		claim, err := h.store.ClaimGitHubTrigger(t.Context(), h.account)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.AdmitGitHubTrigger(t.Context(), claim); !errors.Is(err, ErrLimit) {
			t.Fatal("GitHub bypassed run cap")
		}
		if _, err := h.store.ClaimGitHubCheck(t.Context(), h.account); !errors.Is(err, ErrNoGitHubWork) {
			t.Fatal("failed admission leaked a Check intent")
		}
	})
	t.Run("trigger-and-binding-ceilings-allow-recovery", func(t *testing.T) {
		h := factory(t)
		_, binding := h.bind(t)
		for i := 1; i < api.CIGitHubMaxBindingsPerAccount; i++ {
			req := binding.GitHubBindingRequest
			req.PushBranches = []string{"other"}
			req.PullRequestBranches = nil
			if _, err := h.store.PutGitHubBinding(t.Context(), h.account, fmt.Sprintf("binding-%d", i), req); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := h.store.PutGitHubBinding(t.Context(), h.account, "overflow", binding.GitHubBindingRequest); !errors.Is(err, ErrLimit) {
			t.Fatal("binding cap missing")
		}
		for i := 1; i <= api.CIGitHubMaxTriggersPerAccount; i++ {
			event := sourceEvent()
			event.CommitSHA = fmt.Sprintf("%040x", i)
			if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), event); err != nil {
				t.Fatal(err)
			}
		}
		event := sourceEvent()
		event.CommitSHA = fmt.Sprintf("%040x", 1)
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), event); err != nil {
			t.Fatal("quota prevented source recovery")
		}
		event.CommitSHA = strings.Repeat("f", 40)
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), event); !errors.Is(err, ErrLimit) {
			t.Fatal("trigger cap missing")
		}
	})
	t.Run("maximum-filter-snapshot-is-admissible", func(t *testing.T) {
		h := factory(t)
		_, binding := h.bind(t)
		req := binding.GitHubBindingRequest
		req.PushBranches, req.PullRequestBranches = nil, nil
		for i := range api.CIGitHubMaxBranches {
			branch := strings.Repeat("<", api.CIGitHubBranchMaxBytes-4) + fmt.Sprintf("%04d", i)
			req.PushBranches = append(req.PushBranches, branch)
			req.PullRequestBranches = append(req.PullRequestBranches, branch)
		}
		if _, err := h.store.PutGitHubBinding(t.Context(), h.account, "checks", req); err != nil {
			t.Fatal(err)
		}
		event := sourceEvent()
		event.Branch = req.PushBranches[0]
		if err := h.store.RecordGitHubDelivery(t.Context(), h.account, uuid.NewString(), event); err != nil {
			t.Fatalf("accepted filters could not be snapshotted: %v", err)
		}
		if _, err := h.store.ClaimGitHubTrigger(t.Context(), h.account); err != nil {
			t.Fatal(err)
		}
	})
}

func TestGitHubSourcesLegacyAccount(t *testing.T) {
	account := strings.ReplaceAll(uuid.NewString(), "-", "")
	s := NewMemoryStore()
	h := &gitHubHarness{store: s, account: account}
	h.bind(t)
	if err := s.RecordGitHubDelivery(t.Context(), githubConfig(account, false).GitHubAccounts()[0], uuid.NewString(), sourceEvent()); err != nil {
		t.Fatal(err)
	}
	if worked, err := AdmitGitHubOnce(t.Context(), githubConfig(account, false), s, func(context.Context, string, int64, int64, string) error { return nil }); err != nil || !worked {
		t.Fatalf("legacy account could not admit GitHub receipt: %v", err)
	}
}
