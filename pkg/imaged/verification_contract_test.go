package imaged

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

const testVerifyContract = `version: 1
timeout_seconds: 10
checks:
  - name: products
    http:
      path: /api/products
      expect:
        status: 200
        json: {/count: 2}
  - name: create-order
    http:
      method: POST
      path: /api/orders
      json: {sku: a}
      expect: {status: 201}
  - name: orphans
    sql:
      database: main
      query: select count(*) from orders where customer_id is null
      expect: {value: "0"}
`

type contractGateway struct {
	mu       sync.Mutex
	requests []string
	products int
}

func (g *contractGateway) handler(candidateID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.requests = append(g.requests, r.Method+" "+r.URL.Path)
		products := g.products
		g.mu.Unlock()
		w.Header().Set(apihostingreceipt.ServedDeploymentHeader, candidateID)
		w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(candidateID, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
		switch r.URL.Path {
		case "/api/products":
			w.WriteHeader(products)
			_, _ = w.Write([]byte(`{"count":2}`))
		case "/api/orders":
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
}

func (g *contractGateway) sent(prefix string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.requests {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

// fakeQueryWorker plays apid's role for SQL checks.
func fakeQueryWorker(ctx context.Context, store *state.MemStore, value string) {
	for ctx.Err() == nil {
		claimed, _ := store.ClaimVerificationQueries(ctx, time.Now(), 10)
		for _, q := range claimed {
			q.RowCount, q.HasFirst, q.FirstValue = 1, true, &value
			_ = store.CompleteVerificationQuery(ctx, q)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestApplyVerificationContractGatesTheCandidate(t *testing.T) {
	for _, tc := range []struct {
		name         string
		scope        string
		products     int
		sqlValue     string
		released     bool
		wantStatus   string
		wantPost     bool
		wantReport   bool
		wantBlocking []string
	}{
		{name: "production passes and skips the POST", scope: state.DefaultEnvScope, products: 200, sqlValue: "0", released: true, wantStatus: apihostingreceipt.SmokeVerified, wantReport: true},
		{name: "failing route blocks promotion", scope: state.DefaultEnvScope, products: 500, sqlValue: "0", released: true, wantStatus: apihostingreceipt.SmokeFailed, wantReport: true, wantBlocking: []string{"products"}},
		{name: "failing SQL invariant blocks promotion", scope: state.DefaultEnvScope, products: 200, sqlValue: "3", released: true, wantStatus: apihostingreceipt.SmokeFailed, wantReport: true, wantBlocking: []string{"orphans"}},
		{name: "environment branch sends the POST", scope: "feature-x", products: 200, sqlValue: "0", released: true, wantStatus: apihostingreceipt.SmokeVerified, wantPost: true, wantReport: true},
		{name: "not released runs nothing", scope: state.DefaultEnvScope, products: 500, sqlValue: "3", released: false, wantStatus: apihostingreceipt.SmokeVerified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "verify-contract@example.test", "pro")
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "shop", RAMMB: 256, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Scope: tc.scope})
			if err != nil {
				t.Fatal(err)
			}
			contract, err := verifycontract.ParseYAML([]byte(testVerifyContract))
			if err != nil {
				t.Fatal(err)
			}
			raw, sum, _ := contract.Canonical()
			if _, err := store.PutAppVerificationContract(ctx, state.VerificationContract{AppID: app.ID, AccountID: account.ID, Contract: raw, SHA256: sum, CheckCount: len(contract.Checks)}, nil); err != nil {
				t.Fatal(err)
			}
			go fakeQueryWorker(ctx, store, tc.sqlValue)

			gw := &contractGateway{products: tc.products}
			server := httptest.NewServer(gw.handler(candidate.ID))
			defer server.Close()
			cv := ContractVerification{
				Verifier: apihostingreceipt.Verifier{BaseURL: server.URL, RequestTimeout: time.Second, RetryInterval: 5 * time.Millisecond,
					Authorize: func(context.Context, string, string, time.Time) error { return nil }},
				Store:     store,
				Released:  func(platformflags.Subject) bool { return tc.released },
				QueryPoll: 5 * time.Millisecond,
			}
			in := apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, Path: "/healthz"}
			smoke, err := ApplyVerificationContract(ctx, cv, app, candidate, in)
			if err != nil {
				t.Fatal(err)
			}
			if smoke.Status != tc.wantStatus {
				t.Fatalf("status = %s (%s), want %s; report=%+v", smoke.Status, smoke.Error, tc.wantStatus, smoke.VerificationContract)
			}
			if gw.sent("POST") != tc.wantPost {
				t.Fatalf("POST sent = %v, want %v", gw.sent("POST"), tc.wantPost)
			}
			if (smoke.VerificationContract != nil) != tc.wantReport {
				t.Fatalf("report present = %v", smoke.VerificationContract != nil)
			}
			if !tc.wantReport {
				if gw.sent("GET /api") {
					t.Fatal("unreleased contract sent requests")
				}
				return
			}
			report := smoke.VerificationContract
			if report.ContractSHA256 != sum {
				t.Fatalf("report contract sha = %q", report.ContractSHA256)
			}
			if got := report.Blocking(); strings.Join(got, ",") != strings.Join(tc.wantBlocking, ",") {
				t.Fatalf("blocking = %v, want %v; report=%+v", got, tc.wantBlocking, report)
			}
			if len(tc.wantBlocking) > 0 && smoke.ErrorCode != apihostingreceipt.SmokeErrorVerificationContractFailed {
				t.Fatalf("error code = %q", smoke.ErrorCode)
			}
			if tc.scope == state.DefaultEnvScope && report.Checks[1].Status != verifycontract.StatusSkipped {
				t.Fatalf("production POST check = %+v", report.Checks[1])
			}
			if _, err := apihostingreceipt.Encode(buildHostingReceipt(app, candidate, smoke)); err != nil {
				t.Fatalf("receipt with report does not encode: %v", err)
			}
		})
	}
}
