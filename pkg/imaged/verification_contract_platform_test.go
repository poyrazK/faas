package imaged

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

// A contract whose only blocking check could not run for a platform reason
// is unavailable evidence the recovery window retries, not a failed
// deployment.
func TestApplyVerificationContractRetriesPlatformOnlyFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "verify-platform@example.test", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "shop", RAMMB: 256, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Scope: state.DefaultEnvScope})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := verifycontract.ParseYAML([]byte("version: 1\nchecks:\n  - {name: orphans, sql: {database: main, query: select 1, expect: {value: \"1\"}}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	raw, sum, _ := contract.Canonical()
	if _, err := store.PutAppVerificationContract(ctx, state.VerificationContract{AppID: app.ID, AccountID: account.ID, Contract: raw, SHA256: sum, CheckCount: 1}, nil); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var names []string
	go func() {
		for ctx.Err() == nil {
			claimed, _ := store.ClaimVerificationQueries(ctx, time.Now(), 10)
			for _, q := range claimed {
				mu.Lock()
				names = append(names, q.CheckName)
				mu.Unlock()
				q.ErrorCode, q.Error = verifycontract.CodeQueryUnavailable, "managed Postgres is not available"
				_ = store.CompleteVerificationQuery(ctx, q)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	gw := &contractGateway{products: http.StatusOK}
	server := httptest.NewServer(gw.handler(candidate.ID))
	defer server.Close()
	cv := ContractVerification{
		Verifier: apihostingreceipt.Verifier{BaseURL: server.URL, RequestTimeout: time.Second, RetryInterval: 5 * time.Millisecond,
			Authorize: func(context.Context, string, string, time.Time) error { return nil }},
		Store: store, Released: func(platformflags.Subject) bool { return true }, QueryPoll: 5 * time.Millisecond,
	}
	smoke, err := ApplyVerificationContract(ctx, cv, app, candidate, apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified})
	var unavailable *apihostingreceipt.VerificationUnavailableError
	if !errors.As(err, &unavailable) || smoke.Status != apihostingreceipt.SmokeSkipped || smoke.ErrorCode != apihostingreceipt.SmokeErrorContractUnavailable {
		t.Fatalf("smoke=%+v err=%v, want retryable unavailable", smoke, err)
	}
	if smoke.VerificationContract == nil || smoke.VerificationContract.Checks[0].Code != verifycontract.CodeQueryUnavailable {
		t.Fatalf("report = %+v", smoke.VerificationContract)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(names) != 1 || names[0] != "orphans" {
		t.Fatalf("handoff rows named %q, want the check's name", names)
	}
}
