package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261116191802 A2: a rollout parks the guests a candidate vmmd refused
// through the operator recovery intent the owning schedd executes, written
// from the control plane under the node-rollout actor.
func TestInstancesForceParkBreakGlassDBSubmitsAndWaits(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tenant@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "refused-guest", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	newInstance := func() string {
		t.Helper()
		inst, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		return inst.ID
	}

	var notified []string
	// Force-park intents settle through schedd's operator park protocol,
	// which MemStore enforces; the fake schedd records the outcome here and
	// the command reads it through the intent seam.
	var outcomes sync.Map
	old := instancesBreakGlassDBOpener
	oldOut, oldErr := osStdout, osStderr
	t.Cleanup(func() {
		instancesBreakGlassDBOpener = old
		osStdout, osStderr = oldOut, oldErr
	})
	instancesBreakGlassDBOpener = func() (instancesBreakGlassDB, error) {
		return instancesBreakGlassDB{
			store: store,
			notify: func(_ context.Context, channel, payload string) error {
				notified = append(notified, channel+" "+payload)
				return nil
			},
			intent: func(ctx context.Context, id string) (state.OperatorIntent, error) {
				intent, err := store.GetOperatorIntent(ctx, id)
				if err == nil {
					if status, ok := outcomes.Load(id); ok {
						intent.Status = status.(state.OperatorIntentStatus)
					}
				}
				return intent, err
			},
			close: func() {},
		}, nil
	}
	var out, stderr bytes.Buffer
	osStdout, osStderr = &out, &stderr

	// schedd stands in: claim the pending intent and settle it.
	settle := func(succeed bool) <-chan state.OperatorIntent {
		done := make(chan state.OperatorIntent, 1)
		go func() {
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				intent, err := store.ClaimPendingOperatorIntent(ctx)
				if errors.Is(err, state.ErrOperatorIntentNotFound) || errors.Is(err, state.ErrNotFound) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				if err != nil {
					t.Error(err)
					close(done)
					return
				}
				if succeed {
					outcomes.Store(intent.ID, state.OperatorIntentSucceeded)
				} else {
					outcomes.Store(intent.ID, state.OperatorIntentFailed)
				}
				done <- intent
				return
			}
			close(done)
		}()
		return done
	}

	parked := newInstance()
	claimed := settle(true)
	code := cmdInstancesForcePark([]string{"--instance-id", parked, "--reason", "node_join_unadoptable", "--break-glass-db", "--timeout", "5s", "--yes"})
	intent := <-claimed
	if code != 0 {
		t.Fatalf("succeeded park code=%d stderr=%s", code, stderr.String())
	}
	if intent.Kind != state.OperatorIntentKindForcePark || intent.ActorID != nodeRolloutActorID || intent.Reason != "node_join_unadoptable" {
		t.Fatalf("intent = kind %q actor %q reason %q, want force-park under the node-rollout actor", intent.Kind, intent.ActorID, intent.Reason)
	}
	if len(notified) != 1 || !strings.HasPrefix(notified[0], db.NotifyOperatorIntent+" ") || !strings.Contains(notified[0], intent.ID) {
		t.Fatalf("notifications = %q, want one %s carrying the intent", notified, db.NotifyOperatorIntent)
	}

	// A park schedd could not complete leaves the guest refused: exit 1.
	failing := newInstance()
	claimed = settle(false)
	if code := cmdInstancesForcePark([]string{"--instance-id", failing, "--reason", "node_join_unadoptable", "--break-glass-db", "--timeout", "5s", "--yes"}); code != 1 {
		t.Fatalf("failed park code=%d, want 1", code)
	}
	<-claimed

	// Nothing settles it: the command gives up at --timeout.
	stuck := newInstance()
	if code := cmdInstancesForcePark([]string{"--instance-id", stuck, "--reason", "node_join_unadoptable", "--break-glass-db", "--timeout", "300ms", "--yes"}); code != 1 {
		t.Fatalf("unsettled park code=%d, want 1", code)
	}

	for _, args := range [][]string{
		{"--instance-id", parked, "--reason", "x", "--break-glass-db", "--break-glass-local", "--yes"},
		{"--instance-id", parked, "--break-glass-db", "--yes"},
		{"--instance-id", parked, "--reason", "x", "--break-glass-db", "--request-key", "k", "--yes"},
	} {
		if code := cmdInstancesForcePark(args); code != 2 {
			t.Errorf("args %v code=%d, want 2", args, code)
		}
	}
}
