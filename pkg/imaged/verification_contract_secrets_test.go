package imaged

import (
	"context"
	"errors"
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

const secretRefContract = `version: 1
timeout_seconds: 10
checks:
  - name: me
    http:
      path: /api/me
      headers:
        Authorization: "Bearer ${secret:API_TOKEN}"
`

// fakeSecretd records the lease it saw and answers from values.
type fakeSecretd struct {
	store     *state.MemStore
	values    map[string]string
	err       error
	mu        sync.Mutex
	sawLease  bool
	requested []string
}

func (f *fakeSecretd) OpenVerificationSecrets(_ context.Context, _, deploymentID string, keys []string) (map[string]string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lease, ok := f.store.DeploymentVerificationLease(deploymentID)
	f.sawLease = ok && lease.NodeName == "compute-7" && lease.ExpiresAt.After(time.Now())
	f.requested = keys
	if f.err != nil {
		return nil, nil, f.err
	}
	out := map[string]string{}
	var missing []string
	for _, k := range keys {
		if v, ok := f.values[k]; ok {
			out[k] = v
		} else {
			missing = append(missing, k)
		}
	}
	return out, missing, nil
}

// ADR-20261122220000 amendment 2: a header's ${secret:NAME} is opened
// through secretd under this node's lease and sent only to the candidate.
func TestApplyVerificationContractSecretHeaders(t *testing.T) {
	for _, tc := range []struct {
		name       string
		values     map[string]string
		secretdErr error
		noSecretd  bool
		wantStatus string
		wantCode   string
		wantErr    bool
	}{
		{name: "secret sent to the candidate", values: map[string]string{"API_TOKEN": "s3cr3t-tok"}, wantStatus: apihostingreceipt.SmokeVerified},
		{name: "unset secret blocks with its name", values: map[string]string{}, wantStatus: apihostingreceipt.SmokeFailed, wantCode: verifycontract.CodeSecretMissing},
		{name: "secretd unavailable retries", secretdErr: errors.New("unavailable"), wantStatus: apihostingreceipt.SmokeSkipped, wantErr: true},
		{name: "node without secretd retries", noSecretd: true, wantStatus: apihostingreceipt.SmokeSkipped, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "verify-secrets@example.test", "pro")
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
			contract, err := verifycontract.ParseYAML([]byte(secretRefContract))
			if err != nil {
				t.Fatal(err)
			}
			raw, sum, _ := contract.Canonical()
			if _, err := store.PutAppVerificationContract(ctx, state.VerificationContract{AppID: app.ID, AccountID: account.ID, Contract: raw, SHA256: sum, CheckCount: 1}, nil); err != nil {
				t.Fatal(err)
			}
			var gotAuth string
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/me" {
					mu.Lock()
					gotAuth = r.Header.Get("Authorization")
					mu.Unlock()
				}
				w.Header().Set(apihostingreceipt.ServedDeploymentHeader, candidate.ID)
				w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(candidate.ID, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			secretd := &fakeSecretd{store: store, values: tc.values, err: tc.secretdErr}
			cv := ContractVerification{
				Verifier: apihostingreceipt.Verifier{BaseURL: server.URL, RequestTimeout: time.Second, RetryInterval: 5 * time.Millisecond,
					Authorize: func(context.Context, string, string, time.Time) error { return nil }},
				Store:    store,
				Released: func(platformflags.Subject) bool { return true },
				Secrets:  secretd,
				NodeName: "compute-7",
			}
			if tc.noSecretd {
				cv.Secrets = nil
			}
			in := apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, Path: "/healthz"}
			smoke, err := ApplyVerificationContract(ctx, cv, app, candidate, in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if smoke.Status != tc.wantStatus {
				t.Fatalf("status = %s (%s), want %s", smoke.Status, smoke.Error, tc.wantStatus)
			}
			if _, held := store.DeploymentVerificationLease(candidate.ID); held {
				t.Fatal("lease outlived the verification")
			}
			if tc.noSecretd {
				return
			}
			if !secretd.sawLease || strings.Join(secretd.requested, ",") != "API_TOKEN" {
				t.Fatalf("secretd saw lease %v, keys %v", secretd.sawLease, secretd.requested)
			}
			if tc.wantStatus == apihostingreceipt.SmokeVerified && gotAuth != "Bearer s3cr3t-tok" {
				t.Fatalf("Authorization = %q", gotAuth)
			}
			if tc.wantCode != "" {
				check := smoke.VerificationContract.Checks[0]
				if check.Code != tc.wantCode || !strings.Contains(strings.Join(check.Failures, " "), "API_TOKEN") {
					t.Fatalf("check = %+v", check)
				}
			}
			if smoke.VerificationContract != nil {
				encoded, err := apihostingreceipt.Encode(buildHostingReceipt(app, candidate, smoke))
				if err != nil || strings.Contains(string(encoded), "s3cr3t-tok") {
					t.Fatalf("receipt encode = %v or carries the secret", err)
				}
			}
		})
	}
}
