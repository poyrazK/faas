// adr: 460 — the production outbox and PostgreSQL replay the same candidate.
package imaged

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgHostingPublicationOutboxRecoversAfterRestart(t *testing.T) {
	testHostingOutboxRecovery(t, "publication")
}

// adr: 461 — gateway and transport outages share the durable recovery path.
func TestPgHostingGatewayOutboxRecoversAfterRestart(t *testing.T) {
	testHostingOutboxRecovery(t, "gateway")
}

func TestPgHostingTransportOutboxRecoversAfterRestart(t *testing.T) {
	testHostingOutboxRecovery(t, "transport")
}

func testHostingOutboxRecovery(t *testing.T, failure string) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "hosting-outbox@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "hosting-outbox", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	previous, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:previous"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"deployment_id":%q,"storage_key":%q,"mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`, candidate.ID, "snap/"+candidate.ID+"/mem")
	var outboxID int64
	if err := pool.QueryRow(ctx, `INSERT INTO notification_outbox (channel, payload, available_at) VALUES ($1, $2, now()) RETURNING id`, db.NotifySnapshotWritten, payload).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var recovered atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if failure == "gateway" && !recovered.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set(apihostingreceipt.ServedDeploymentHeader, candidate.ID)
		w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(candidate.ID, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	publications := 0
	var secret string
	client := &http.Client{Transport: hostingRecoveryTransport(func(r *http.Request) (*http.Response, error) {
		if failure == "transport" && !recovered.Load() {
			return nil, fmt.Errorf("transport unavailable token=%s", r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader))
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	verifier := apihostingreceipt.Verifier{BaseURL: srv.URL, Client: client, Timeout: time.Second, RetryInterval: 2 * time.Second, Authorize: func(_ context.Context, _, token string, _ time.Time) error {
		publications++
		secret = token
		if failure == "publication" && publications == 1 {
			return fmt.Errorf("publisher temporarily unavailable token=%s", token)
		}
		return nil
	}}
	now := time.Now().UTC()
	newHandler := func() *Handler {
		h := New(state.NewPgStore(pool), &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithHostingSmokeRequired(true)
		h.hostingVerificationNow = func() time.Time { return now }
		return h.WithHostingSmoke(func(ctx context.Context, app state.App, dep state.Deployment) (apihostingreceipt.SmokeResult, error) {
			return VerifyHostingDeployment(ctx, verifier, app, dep)
		})
	}
	firstHandler := newHandler()
	delivered, err := db.DrainNotificationOutboxOnce(ctx, pool, "imaged", []string{db.NotifySnapshotWritten}, firstHandler.HandleNotification, nil)
	firstRequests := requests.Load()
	if err != nil || delivered != 0 || publications != 1 || (failure != "gateway" && firstRequests != 0) || (failure == "gateway" && firstRequests == 0) {
		t.Fatalf("%s outage was not queued: delivered=%d publications=%d requests=%d err=%v", failure, delivered, publications, requests.Load(), err)
	}
	var outboxState, lastError string
	if err := pool.QueryRow(ctx, `SELECT state, COALESCE(last_error, '') FROM notification_outbox WHERE id=$1`, outboxID).Scan(&outboxState, &lastError); err != nil {
		t.Fatal(err)
	}
	dep, err := store.DeploymentByID(ctx, candidate.ID)
	live, liveErr := store.LiveDeploymentForScope(ctx, app.ID, state.DefaultEnvScope)
	var first state.StageState
	decodeErr := json.Unmarshal(dep.StageState, &first)
	wantCode := map[string]string{"publication": apihostingreceipt.SmokeErrorAuthorizationUnavailable, "gateway": apihostingreceipt.SmokeErrorGatewayUnavailable, "transport": apihostingreceipt.SmokeErrorTransportUnavailable}[failure]
	if err != nil || liveErr != nil || decodeErr != nil || dep.Status != state.DeploySnapshotting || len(dep.APIHostingReceipt) != 0 || live.ID != previous.ID || outboxState != "pending" || secret == "" || strings.Contains(lastError, secret) || first.HostingVerification == nil || first.HostingVerification.RetryNotBefore == nil || first.HostingVerification.LastErrorCode != wantCode {
		t.Fatalf("outage lost candidate/traffic/progress: status=%s live=%s outbox=%s progress=%+v err=%v liveErr=%v decode=%v", dep.Status, live.ID, outboxState, first, err, liveErr, decodeErr)
	}
	now = *first.HostingVerification.RetryNotBefore
	recovered.Store(true)
	if _, err := pool.Exec(ctx, `UPDATE notification_outbox SET available_at=now() WHERE id=$1`, outboxID); err != nil {
		t.Fatal(err)
	}
	restartedHandler := newHandler()
	delivered, err = db.DrainNotificationOutboxOnce(ctx, pool, "imaged", []string{db.NotifySnapshotWritten}, restartedHandler.HandleNotification, nil)
	if err != nil || delivered != 1 || publications != 2 || requests.Load() != firstRequests+1 {
		t.Fatalf("outbox recovery did not deliver: delivered=%d publications=%d requests=%d err=%v", delivered, publications, requests.Load(), err)
	}
	dep, err = store.DeploymentByID(ctx, candidate.ID)
	live, liveErr = store.LiveDeploymentForScope(ctx, app.ID, state.DefaultEnvScope)
	receipt, receiptErr := apihostingreceipt.Decode(dep.APIHostingReceipt)
	var last state.StageState
	decodeErr = json.Unmarshal(dep.StageState, &last)
	if err != nil || liveErr != nil || receiptErr != nil || decodeErr != nil || dep.Status != state.DeployLive || live.ID != candidate.ID || receipt.Smoke.Status != apihostingreceipt.SmokeVerified || last.HostingVerification == nil || last.HostingVerification.Attempts != 2 || !last.HostingVerification.DeadlineAt.Equal(first.HostingVerification.DeadlineAt) {
		t.Fatalf("same candidate not verified/promoted: status=%s live=%s smoke=%+v progress=%+v err=%v liveErr=%v receipt=%v decode=%v", dep.Status, live.ID, receipt.Smoke, last, err, liveErr, receiptErr, decodeErr)
	}
}
