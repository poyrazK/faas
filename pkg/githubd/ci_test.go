package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func ciCheckFixture() ci.GitHubCheck {
	return ci.GitHubCheck{AccountID: uuid.NewString(), RunID: uuid.NewString(), Pipeline: "checks", Source: ci.GitHubSource{BindingID: uuid.NewString(), DeliveryID: uuid.NewString(), Generation: 1,
		GitHubEvent: ci.GitHubEvent{Kind: "push", InstallationID: 41, RepositoryID: 123, Repository: "gregale/api", CommitSHA: strings.Repeat("a", 40), Branch: "main"}}}
}

func TestCICheckRecoversLostAcknowledgementWithoutDeploymentIdentity(t *testing.T) {
	check := ciCheckFixture()
	creates, patches, reads := 0, 0, 0
	var remote map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ci-install-token" {
			t.Error("incorrect installation token")
		}
		if r.Method == http.MethodGet {
			reads++
			if r.URL.Query().Get("check_name") != check.Name() {
				t.Error("CI check name not scoped to binding")
			}
			if remote == nil {
				_, _ = w.Write([]byte(`{"check_runs":[]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []any{map[string]any{"id": 77, "name": remote["name"], "external_id": remote["external_id"], "head_sha": remote["head_sha"]}}})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["conclusion"] != "action_required" || body["status"] != "completed" || body["external_id"] != check.ExternalID() {
			t.Error("CI check incorrectly claimed success or lost source identity")
		}
		if r.Method == http.MethodPost {
			creates++
			remote = body
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{`))
			return // creation succeeded; its response is lost.
		}
		patches++
		if r.URL.Path != "/repos/gregale/api/check-runs/77" || body["head_sha"] != nil {
			t.Error("recovery did not PATCH the original Check")
		}
		_, _ = w.Write([]byte(`{"id":77}`))
	}))
	defer api.Close()
	tokens := NewTokenCache(fakeFetcher(func(_ context.Context, id int64) (string, time.Time, error) {
		if id != 41 {
			t.Error("foreign installation token requested")
		}
		return "ci-install-token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	writer := &ChecksAPI{Tokens: tokens, HTTP: &singleHostClient{base: api.Client(), api: api.URL}}
	if _, err := writer.WriteBlockedCICheck(t.Context(), check); !errors.Is(err, ci.ErrGitHubUnavailable) {
		t.Fatalf("lost acknowledgement did not request recovery: %v", err)
	}
	id, err := writer.WriteBlockedCICheck(t.Context(), check)
	if err != nil || id != 77 || creates != 1 || patches != 1 || reads != 2 {
		t.Fatalf("duplicate or unrecoverable Check: id=%d creates=%d patches=%d reads=%d err=%v", id, creates, patches, reads, err)
	}
	other := check
	other.Source.BindingID = uuid.NewString()
	if other.Name() == check.Name() {
		t.Fatal("CI bindings share Check names")
	}
}

func TestCIRepositoryVerificationRechecksDurableInstallationOwnership(t *testing.T) {
	account := uuid.NewString()
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"id":123,"full_name":"gregale/api"}`))
	}))
	defer api.Close()
	installs := newMemStoreInstalls()
	if err := installs.Upsert(t.Context(), state.GitHubInstall{AccountID: account, InstallationID: 41, AuditGithubLogin: "gregale"}); err != nil {
		t.Fatal(err)
	}
	tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
		return "ci-install-token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	svc := &RealService{Auth: &AppAuth{HTTPClient: &singleHostClient{base: api.Client(), api: api.URL}}, Tokens: tokens, Installs: installs}
	if err := svc.VerifyCIRepository(t.Context(), account, 41, 123, "gregale/api"); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyCIRepository(t.Context(), uuid.NewString(), 41, 123, "gregale/api"); !errors.Is(err, ci.ErrGitHubAccess) || calls != 1 {
		t.Fatal("cached token bypassed account ownership")
	}
	if err := svc.VerifyCIRepository(t.Context(), account, 42, 123, "gregale/api"); !errors.Is(err, ci.ErrGitHubAccess) || calls != 1 {
		t.Fatal("another installation was substituted")
	}
	if err := svc.VerifyCIRepository(t.Context(), account, 41, 999, "gregale/api"); !errors.Is(err, ci.ErrGitHubAccess) {
		t.Fatal("repository name substituted for immutable ID")
	}
}

func TestCIInboxRunsOnlyAfterHMACAndRetriesBeforeAcknowledgement(t *testing.T) {
	secret := []byte("synthetic webhook secret")
	body := []byte(`{"ref":"refs/heads/main","after":"` + strings.Repeat("a", 40) + `","repository":{"id":123,"full_name":"gregale/api"},"installation":{"id":41}}`)
	store := &recordingDeliveryStore{}
	journalCalls := 0
	srv := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), WebhookSecret: secret, Deliveries: store, RecordCI: func(context.Context, WebhookDelivery) error {
		journalCalls++
		if journalCalls == 1 {
			return ci.ErrGitHubUnavailable
		}
		return nil
	}}
	call := func(sig string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
		req.Header.Set("X-Hub-Signature-256", sig)
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", uuid.NewString())
		w := httptest.NewRecorder()
		srv.WebhookLoopbackHandler().ServeHTTP(w, req)
		return w
	}
	if got := call("sha256=bad"); got.Code != http.StatusUnauthorized || journalCalls != 0 {
		t.Fatal("unverified payload reached CI inbox")
	}
	if got := call(SignForTest(body, secret)); got.Code != http.StatusServiceUnavailable || journalCalls != 1 {
		t.Fatal("journal failure was acknowledged")
	}
	if got := call(SignForTest(body, secret)); got.Code != http.StatusAccepted || journalCalls != 2 {
		t.Fatal("signed retry could not recover source journal")
	}
}

func TestCIRateLimitsRemainRetryable(t *testing.T) {
	for _, header := range []http.Header{{"X-Ratelimit-Remaining": []string{"0"}}, {"Retry-After": []string{"30"}}} {
		if err := ciGitHubStatus(&http.Response{StatusCode: http.StatusForbidden, Header: header}); !errors.Is(err, ci.ErrGitHubUnavailable) {
			t.Fatal("rate limit was mistaken for revoked access")
		}
	}
	if err := ciGitHubStatus(&http.Response{StatusCode: http.StatusForbidden}); !errors.Is(err, ci.ErrGitHubAccess) {
		t.Fatal("revoked permission was not rejected")
	}
}

func TestCISignedEventCreatesExactlyOneBlockedReceipt(t *testing.T) {
	account := uuid.NewString()
	store := ci.NewMemoryStore()
	definition := ci.Definition{Version: 1, Repository: "gregale/api", Jobs: map[string]ci.Job{"test": {Image: "registry.example/ci@sha256:" + strings.Repeat("a", 64), Steps: []ci.Step{{Name: "unit", Run: "stored only"}}}}}
	revision, err := store.PutRevision(t.Context(), account, "checks", definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutGitHubBinding(t.Context(), account, "checks", ci.GitHubBindingRequest{RevisionID: revision.ID, InstallationID: 41, RepositoryID: 123, PushBranches: []string{"main"}}); err != nil {
		t.Fatal(err)
	}
	cfg := ci.ConfigFromEnv(func(key string) string {
		if key == ci.AccountsEnv {
			return account
		}
		if key == ci.InternalEnv || key == ci.GitHubEnv {
			return "1"
		}
		return ""
	})
	secret := []byte("synthetic webhook secret")
	delivery := uuid.NewString()
	body := []byte(`{"ref":"refs/heads/main","after":"` + strings.Repeat("a", 40) + `","repository":{"id":123,"full_name":"gregale/api"},"installation":{"id":41}}`)
	srv := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), WebhookSecret: secret, Deliveries: &recordingDeliveryStore{}, RecordCI: func(ctx context.Context, d WebhookDelivery) error {
		return ci.RecordVerifiedGitHubDelivery(ctx, cfg, store, d.DeliveryID, d.EventType, d.Payload)
	}}
	for range 3 {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", delivery)
		req.Header.Set("X-Hub-Signature-256", SignForTest(body, secret))
		w := httptest.NewRecorder()
		srv.WebhookLoopbackHandler().ServeHTTP(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("verified source rejected: %d %s", w.Code, w.Body)
		}
	}
	verify := func(_ context.Context, acct string, installation, repository int64, name string) error {
		if acct != account || installation != 41 || repository != 123 || name != "gregale/api" {
			return ci.ErrGitHubAccess
		}
		return nil
	}
	if worked, err := ci.AdmitGitHubOnce(t.Context(), cfg, store, verify); err != nil || !worked {
		t.Fatalf("verified event not admitted: %v", err)
	}
	if worked, err := ci.AdmitGitHubOnce(t.Context(), cfg, store, verify); err != nil || worked {
		t.Fatal("redelivery admitted another run")
	}
	runs, err := store.ListRuns(t.Context(), account)
	if err != nil || len(runs) != 1 || runs[0].Status != ci.StatusBlocked || runs[0].BlockedReason != ci.ComputeUnavailable || runs[0].CommitSHA != strings.Repeat("a", 40) || runs[0].RevisionID != revision.ID || runs[0].GitHub == nil {
		t.Fatalf("signed webhook escaped blocked admission: %+v %v", runs, err)
	}
}

type ciLegacySecretResolver struct{ secret []byte }

func (r ciLegacySecretResolver) Resolve(context.Context, int64) ([]byte, error) { return r.secret, nil }
func (ciLegacySecretResolver) Invalidate(int64)                                 {}

func TestCILegacyInstallationSignerCannotAssertAnotherInstallation(t *testing.T) {
	secret := []byte("synthetic legacy secret")
	body := []byte(`{"ref":"refs/heads/main","after":"` + strings.Repeat("a", 40) + `","repository":{"id":123,"full_name":"gregale/api"},"installation":{"id":99}}`)
	called := false
	srv := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), SecretResolver: ciLegacySecretResolver{secret}, Deliveries: &recordingDeliveryStore{}, RecordCI: func(context.Context, WebhookDelivery) error { called = true; return nil }}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", uuid.NewString())
	req.Header.Set("X-GitHub-Installation-id", "41")
	req.Header.Set("X-Hub-Signature-256", SignForTest(body, secret))
	w := httptest.NewRecorder()
	srv.WebhookLoopbackHandler().ServeHTTP(w, req)
	if w.Code != http.StatusAccepted || called {
		t.Fatal("legacy signer crossed CI's App-authenticated source boundary")
	}
}
