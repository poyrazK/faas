// adr: 20261027090010, 20261027090014
package agentd_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/agentd"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// fakeCommenter records comments and answers with err when set.
type fakeCommenter struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeCommenter) UpsertIssueComment(_ context.Context, account string, installation int64, repository string, number int64, key, body string) (githubdgrpc.IssueComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, account+" "+repository+" "+key+" "+body)
	if f.err != nil {
		return githubdgrpc.IssueComment{}, f.err
	}
	if installation != 42 || number != 7 {
		return githubdgrpc.IssueComment{}, status.Error(codes.InvalidArgument, "wrong target")
	}
	return githubdgrpc.IssueComment{ID: 99, URL: "https://github.com/acme/app/pull/7#issuecomment-99", Created: true}, nil
}

// queueReviewerRun admits a run of an agent that comments on acme/app#7.
func queueReviewerRun(t *testing.T, s *agentstore.Store, name string) string {
	t.Helper()
	ctx := context.Background()
	spec := agentstore.VersionSpec{Harness: agentstore.Harness{Image: "ghcr.io/acme/agent:1", Run: []string{"comment", "{prompt}"}},
		PromptTemplate: "Review", RAMMB: 1024, TimeoutSec: 60, Deliveries: []agentstore.Delivery{{Type: api.AgentDeliveryGitHubComment}}}
	if _, _, err := s.CreateAgent(ctx, account, name, "", spec, 10); err != nil {
		t.Fatal(err)
	}
	run, _, err := s.AdmitRun(ctx, agentstore.Admission{AccountID: account, Agent: name, Trigger: "api",
		GitHub: &agentstore.GitHubTarget{Repository: "acme/app", Number: 7},
		Limits: agentstore.AdmitLimits{RunsPerDay: 100, QueuedMax: 100, TimeoutMaxSec: 3600, RAMMBMax: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	return run.ID
}

func TestRunDeliversAComment(t *testing.T) {
	_, store := openStore(t)
	github := &fakeCommenter{}
	deliverer := agentd.NewDeliverer(store, github, agentd.DeliveryConfig{LeaseTTL: 3 * time.Second}, nil)
	replica := newAgentd(t, store, &fakeSchedd{}).WithDeliveries(deliverer)
	id := queueReviewerRun(t, store, "reviewer")
	if claimed, err := replica.ClaimOnce(context.Background()); err != nil || !claimed {
		t.Fatalf("claim %v %v", claimed, err)
	}
	if run := runState(t, store, id); run.State != agentstore.StateDelivering {
		t.Fatalf("after the harness %+v", run)
	}
	if n, err := deliverer.DeliverOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("deliver %d %v", n, err)
	}
	if len(github.calls) != 1 || !strings.HasPrefix(github.calls[0], account+" acme/app agent-") || !strings.HasSuffix(github.calls[0], " Looks good.") {
		t.Fatalf("comments %q", github.calls)
	}
	run := runState(t, store, id)
	out, err := store.RunOutputs(context.Background(), account, id)
	if run.State != agentstore.StateSucceeded || err != nil || len(out.Deliveries) != 1 || out.Deliveries[0].URL == "" {
		t.Fatalf("run %+v deliveries %+v %v", run, out.Deliveries, err)
	}
}

// A replica that cannot deliver fails a run's declared deliveries at once,
// rather than leaving the run waiting out the delivery window.
func TestRunWithoutDeliveriesFailsItsDeliveries(t *testing.T) {
	_, store := openStore(t)
	id := queueReviewerRun(t, store, "reviewer")
	if claimed, err := newAgentd(t, store, &fakeSchedd{}).ClaimOnce(context.Background()); err != nil || !claimed {
		t.Fatalf("claim %v %v", claimed, err)
	}
	if run := runState(t, store, id); run.State != agentstore.StateFailed || run.OutcomeCode != "delivery_failed" {
		t.Fatalf("run %+v", run)
	}
}

func TestDeliveryOutcomesFollowGithubd(t *testing.T) {
	for name, tc := range map[string]struct {
		err      error
		delivery string
		run      string
	}{
		"no installation": {status.Error(codes.NotFound, "no installation"), api.AgentDeliveryFailed, agentstore.StateFailed},
		"refused":         {status.Error(codes.FailedPrecondition, "locked"), api.AgentDeliveryFailed, agentstore.StateFailed},
		"unavailable":     {status.Error(codes.Unavailable, "down"), api.AgentDeliveryPending, agentstore.StateDelivering},
		"old githubd":     {status.Error(codes.Unimplemented, "unknown method"), api.AgentDeliveryPending, agentstore.StateDelivering},
		"no answer":       {status.Error(codes.DeadlineExceeded, "slow"), api.AgentDeliveryReconciling, agentstore.StateDelivering},
		"lost":            {status.Error(codes.Unknown, "lost"), api.AgentDeliveryReconciling, agentstore.StateDelivering},
	} {
		t.Run(name, func(t *testing.T) {
			_, store := openStore(t)
			deliverer := agentd.NewDeliverer(store, &fakeCommenter{err: tc.err}, agentd.DeliveryConfig{}, nil)
			id := queueReviewerRun(t, store, "reviewer")
			if claimed, err := newAgentd(t, store, &fakeSchedd{}).WithDeliveries(deliverer).ClaimOnce(context.Background()); err != nil || !claimed {
				t.Fatalf("claim %v %v", claimed, err)
			}
			if _, err := deliverer.DeliverOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			out, err := store.RunOutputs(context.Background(), account, id)
			if err != nil || len(out.Deliveries) != 1 || out.Deliveries[0].State != tc.delivery {
				t.Fatalf("deliveries %+v %v", out.Deliveries, err)
			}
			if run := runState(t, store, id); run.State != tc.run {
				t.Fatalf("run %+v", run)
			}
		})
	}
}
