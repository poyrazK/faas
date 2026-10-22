package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestBindingVerificationSharedGraphBudget(t *testing.T) {
	ctx := main.WithBindingVerificationBudget(context.Background(), 2)
	var active, peak atomic.Int32
	started := make(chan struct{}, 12)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		main.RunBindingVerificationJobs(ctx, 3, func(member context.Context, _ int) error {
			main.RunBindingVerificationJobs(member, 4, func(probe context.Context, _ int) error {
				return main.WithBindingVerificationSlot(probe, func() error {
					n := active.Add(1)
					for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
					}
					started <- struct{}{}
					<-release
					active.Add(-1)
					return nil
				})
			})
			return nil
		})
		close(done)
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("independent members did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("graph exceeded shared limit")
	default:
	}
	close(release)
	<-done
	if peak.Load() != 2 || len(started) != 10 {
		t.Fatalf("peak=%d remaining=%d", peak.Load(), len(started))
	}
}

func TestBindingVerificationFailureJoinsCleanupAndStopsAdmissions(t *testing.T) {
	ctx := main.WithBindingVerificationBudget(context.Background(), 1)
	var admitted atomic.Int32
	cleaned := false
	var mu sync.Mutex
	main.RunBindingVerificationJobs(ctx, 8, func(job context.Context, _ int) error {
		return main.WithBindingVerificationSlot(job, func() error {
			admitted.Add(1)
			defer func() { mu.Lock(); cleaned = true; mu.Unlock() }()
			return errors.New("configuration changed")
		})
	})
	// Failure cancels the job context before releasing its probe slot.
	if !cleaned || admitted.Load() != 1 {
		t.Fatalf("cleanup=%t admitted=%d", cleaned, admitted.Load())
	}
}

func TestQueueCanaryCancellationOwnership(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(map[bool]string{true: "reused", false: "created"}[reuse], func(t *testing.T) {
			id, deployment := uuid.NewString(), uuid.NewString()
			now := time.Now().UTC()
			canary := api.QueueBindingCanary{ID: id, Binding: "orders", DeploymentID: deployment, Scope: "production", Status: "published", PublishedAt: now, ExpiresAt: now.Add(time.Minute)}
			var cancelled atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					cancelled.Add(1)
				}
				_ = json.NewEncoder(w).Encode(canary)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			pending := ""
			if reuse {
				pending = id
			}
			_, err := main.ExecuteQueueBindingCanary(ctx, api.NewClient(server.URL, "test"), "api", "orders", deployment, true, time.Second, pending)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error=%v", err)
			}
			want := int32(1)
			if reuse {
				want = 0
			}
			if cancelled.Load() != want {
				t.Fatalf("cancelled=%d want=%d", cancelled.Load(), want)
			}
		})
	}
}

func TestBindingVerificationParallelPreservesFreshEvidence(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		t.Run(map[int]string{1: "serial_expires", 2: "parallel_passes"}[concurrency], func(t *testing.T) {
			f := newBindingPolicyCLIFixture(t)
			f.policy.MaxVerificationAge = "1s"
			f.policy.ObjectStorageRequirements = map[string]string{}
			f.policy.QueueRequirements = map[string]string{"orders": api.QueueEvidenceHandlerCompletion, "events": api.QueueEvidenceHandlerCompletion}
			scope, now := f.policy.Scope, time.Now().UTC()
			f.inventory.Bindings = nil
			for _, binding := range []string{"orders", "events"} {
				f.inventory.Bindings = append(f.inventory.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: binding, Binding: binding, Scope: scope, QueueEnvironment: &scope, State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &now, QueueCanarySupported: true})
			}
			original := f.server.Config.Handler
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/api/bindings/queue-canaries" {
					original.ServeHTTP(w, r)
					return
				}
				var request api.CreateQueueBindingCanaryRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				published := time.Now().UTC()
				timer := time.NewTimer(1100 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-r.Context().Done():
					return
				}
				completed := time.Now().UTC()
				canary := api.QueueBindingCanary{ID: uuid.NewString(), Binding: request.Binding, DeploymentID: request.DeploymentID, Scope: scope, Status: "succeeded", PublishedAt: published, ExpiresAt: published.Add(time.Minute), DeliveredAt: &published, CompletedAt: &completed}
				f.mu.Lock()
				for index := range f.inventory.Bindings {
					if f.inventory.Bindings[index].Binding == request.Binding {
						copy := canary
						f.inventory.Bindings[index].QueueCanary = &copy
					}
				}
				f.mu.Unlock()
				_ = json.NewEncoder(w).Encode(canary)
			})
			ctx, cancel := context.WithTimeout(main.WithBindingVerificationBudget(context.WithValue(context.Background(), main.QueueCanaryConsentKey{}, true), concurrency), 5*time.Second)
			defer cancel()
			report, code := main.ExecuteBindingPolicyVerification(ctx, api.NewClient(f.server.URL, "test"), "api", "", false, false, time.Millisecond, 5*time.Second)
			if concurrency == 2 && (code != 0 || !report.Passed) {
				t.Fatalf("parallel verification failed: %+v code=%d", report, code)
			}
			if concurrency == 1 && (code == 0 || report.Passed || report.ErrorCode != "preflight_blocked") {
				t.Fatalf("serial verification accepted expired evidence: %+v code=%d", report, code)
			}
			if len(report.Probes) != 2 || report.Probes[0].Binding != report.Plan[0].Binding || report.Probes[1].Binding != report.Plan[1].Binding {
				t.Fatalf("non-deterministic results: %+v", report.Probes)
			}
		})
	}
}

func TestBindingVerificationConcurrencyFlagsFailBeforeRequests(t *testing.T) {
	for _, args := range [][]string{
		{"bindings", "verify", "api", "--release-policy", "--concurrency", "0"},
		{"bindings", "verify", "api", "--release-policy", "--concurrency", "9"},
		{"bindings", "verify", "api", "--all", "--concurrency", "2"},
		{"projects", "environments", "release-sets", "verify", "shop", "production", "--file", "unused.json", "--concurrency", "0"},
		{"projects", "environments", "release-sets", "prepare", "shop", "production", "--file", "unused.json", "--concurrency", "2"},
	} {
		f := newBindingPolicyCLIFixture(t)
		code, _, _ := main.CaptureBindingCLI(t, f.server.URL, args...)
		if code == 0 || f.reads != 0 || f.policies != 0 || len(f.requests) != 0 {
			t.Fatalf("invalid flags reached API: %v code=%d", args, code)
		}
	}
}

func TestBindingVerificationReviewedSnapshotCannotReplan(t *testing.T) {
	f := newBindingPolicyCLIFixture(t)
	client := api.NewClient(f.server.URL, "test")
	snapshot, err := main.PrepareBindingPolicyVerification(context.Background(), client, "api", "")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.policy.Revision++
	f.mu.Unlock()
	ctx := context.WithValue(context.Background(), main.BindingVerificationSnapshotKey{}, snapshot)
	report, code := main.ExecuteBindingPolicyVerification(ctx, client, "api", (*snapshot.Inventory()).VerificationDeploymentID, false, true, time.Millisecond, time.Second)
	if code == 0 || report.ErrorCode != "release_policy_changed" || len(f.requests) != 0 {
		t.Fatalf("reviewed policy silently replaced: %+v code=%d writes=%d", report, code, len(f.requests))
	}
}
