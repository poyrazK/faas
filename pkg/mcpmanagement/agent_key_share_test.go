package mcpmanagement

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// One account's agent keys hold at most hostedKeyConnsPerAccount
// connections; the next is refused for that account alone, and reaching the
// limit drops only that account's long-idle connections that hold no plan
// (bug hunt H101).
func TestAgentKeyConnectionsAreCappedPerAccount(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{Hosted: true, APIBase: "https://api.gregale.test"}, keyConns: map[string]*keyConnection{}}
	client := api.NewClient("https://api.gregale.test", "fp_test_agent")
	for i := range hostedKeyConnsPerAccount {
		r.keyConns[tokenHash("alice-"+strconv.Itoa(i))] = &keyConnection{accountID: "alice", inflight: 1, lastUsed: time.Now()}
	}
	if _, _, err := r.holdKeyConnection(tokenHash("alice-new"), "alice", nil, client, nil); !errors.Is(err, errKeyConnectionsPerAccount) {
		t.Fatalf("alice past her share = %v, want errKeyConnectionsPerAccount", err)
	}
	if _, release, err := r.holdKeyConnection(tokenHash("bob-1"), "bob", nil, client, nil); err != nil {
		t.Fatalf("another account was limited: %v", err)
	} else {
		release()
	}

	// Idle past the floor: dropped to make room. Recently used: kept.
	r.mu.Lock()
	for i := range hostedKeyConnsPerAccount {
		c := r.keyConns[tokenHash("alice-"+strconv.Itoa(i))]
		c.inflight = 0
		c.lastUsed = time.Now().Add(-keyConnIdleFloor - time.Second)
		if i == 0 {
			c.lastUsed = time.Now()
		}
	}
	r.mu.Unlock()
	if _, release, err := r.holdKeyConnection(tokenHash("alice-new"), "alice", nil, client, nil); err != nil {
		t.Fatalf("alice with idle connections: %v", err)
	} else {
		release()
	}
	r.mu.Lock()
	_, recent := r.keyConns[tokenHash("alice-0")]
	_, bob := r.keyConns[tokenHash("bob-1")]
	r.mu.Unlock()
	if !recent || !bob {
		t.Fatalf("dropped a recently used connection (%v) or another account's (%v)", !recent, !bob)
	}
}

// A connection holding a plan survives the idle floor, so an agent between
// plan and apply keeps its plan.
func TestAgentKeyConnectionsKeepPlansPastTheIdleFloor(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{Hosted: true, APIBase: "https://api.gregale.test"}, keyConns: map[string]*keyConnection{}}
	m := &management{plans: map[string]*releasePlan{"p": {}}}
	r.keyConns[tokenHash("planner")] = &keyConnection{accountID: "alice", management: m, lastUsed: time.Now().Add(-keyConnIdleFloor - time.Second)}
	r.keyConns[tokenHash("idle")] = &keyConnection{accountID: "alice", lastUsed: time.Now().Add(-keyConnIdleFloor - time.Second)}
	r.mu.Lock()
	r.dropIdleKeyConnectionsLocked(time.Now(), keyConnIdleFloor, "")
	_, planner := r.keyConns[tokenHash("planner")]
	_, idle := r.keyConns[tokenHash("idle")]
	r.mu.Unlock()
	if !planner || idle {
		t.Fatalf("after the idle floor: planner kept %v, idle kept %v; want true, false", planner, idle)
	}
}

// One account holds at most hostedCLIPerAccount of the bridge's CLI slots;
// its next command is refused at once while other accounts still run.
func TestHostedCLIIsSharedPerAccount(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	r := &remoteServer{opts: HTTPOptions{Hosted: true, CLIRunner: func(string) LocalCLI {
		return func(context.Context, []string, []byte) (LocalCLIResult, error) {
			started <- struct{}{}
			<-finish
			return LocalCLIResult{}, nil
		}
	}}}
	done := make(chan error, 1)
	go func() {
		_, err := r.grantCLI("alice", "t1")(context.Background(), nil, nil)
		done <- err
	}()
	<-started
	var ie *inputError
	if _, err := r.grantCLI("alice", "t2")(context.Background(), nil, nil); !errors.As(err, &ie) || ie.code != "cli_busy" {
		t.Fatalf("alice's second command = %v, want cli_busy", err)
	}
	go func() {
		_, _ = r.grantCLI("bob", "t3")(context.Background(), nil, nil)
	}()
	<-started
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !r.cliAccounts.acquire("alice") {
		t.Fatal("alice's slot was not released")
	}
}
