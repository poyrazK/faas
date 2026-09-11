package state

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func memCoverageFixture(t *testing.T) (*MemStore, context.Context, Account, App, Deployment) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "coverage-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "coverage-" + uuid.NewString(), RAMMB: 512, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:coverage"})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, account, app, deployment
}

func TestMemStoreCoverageAccountsAndKeys(t *testing.T) {
	m, ctx, account, _, _ := memCoverageFixture(t)
	hash := []byte("coverage-key-hash")
	key, err := m.CreateAPIKey(ctx, account.ID, hash, "coverage", []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.APIKeyByHash(ctx, hash); err != nil || got.ID != key.ID {
		t.Fatalf("APIKeyByHash = %+v, %v", got, err)
	}
	if gotAccount, gotKey, err := m.AuthenticateKey(ctx, hash); err != nil || gotAccount.ID != account.ID || gotKey.ID != key.ID {
		t.Fatalf("AuthenticateKey = %+v, %+v, %v", gotAccount, gotKey, err)
	}
	if err := m.UpdateAccountPlan(ctx, account.ID, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateAccountProviderCustomerID(ctx, account.ID, "cus_coverage"); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateAccountStripeSubscriptionItem(ctx, account.ID, "si_coverage"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.AccountByProviderCustomerID(ctx, "cus_coverage"); err != nil || got.ID != account.ID {
		t.Fatalf("provider lookup = %+v, %v", got, err)
	}
	if err := m.UpdateAccountProviderCustomerID(ctx, account.ID, "ctm_coverage"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.AccountByProviderCustomerID(ctx, "ctm_coverage"); err != nil || got.ID != account.ID {
		t.Fatalf("paddle lookup = %+v, %v", got, err)
	}
	if _, err := m.APIKeyByHash(ctx, []byte("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing API key error = %v", err)
	}
	if err := m.UpdateAccountPlan(ctx, "missing", api.PlanFree); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account update = %v", err)
	}
	if _, err := m.DeleteAPIKeyReturning(ctx, account.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteAPIKeyReturning(ctx, account.ID, key.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat key delete = %v", err)
	}
	if _, err := m.AccountByProviderCustomerID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing provider lookup = %v", err)
	}
}

func TestMemStoreCoverageAppQuotaAndUpdates(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)
	limits := api.Limits{DeployedApps: 1}
	if _, err := m.CreateAppIfUnderQuota(ctx, App{AccountID: account.ID, Slug: "second"}, limits); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota error = %v", err)
	}
	if _, err := m.CreateAppIfUnderQuota(ctx, App{AccountID: "missing", Slug: "missing"}, limits); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing quota account = %v", err)
	}
	if _, err := m.CreateAppIfUnderQuota(ctx, App{AccountID: account.ID, Slug: app.Slug}, api.Limits{DeployedApps: 10}); !errors.Is(err, ErrConflict) {
		t.Fatalf("quota slug conflict = %v", err)
	}
	egress := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	rootDir := "services/api"
	workloadName := "api"
	startCmd := "node server.js"
	updated, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		RAMMB: &[]int{1024}[0], SetIdleTimeout: true, IdleTimeoutS: new(int),
		SetMinInstances: true, MinInstances: new(int), EgressAllowlist: &egress, SetEgressAllowlist: true,
		SetAutoscaleTargetRPS: true, AutoscaleTargetRPS: new(int), SetAutoscaleTargetCPUPct: true, AutoscaleTargetCPUPct: new(int),
		Manifest: &AppManifest{Port: 8080},
		// Phase 5 widening (PR-E): reconcile populates these on
		// update. Coverage tripwire ensures both stores mirror the
		// same fields.
		RootDir: &rootDir, WorkloadName: &workloadName, StartCommand: &startCmd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.RAMMB != 1024 || updated.Manifest.Port != 8080 || len(updated.EgressAllowlist) != 1 {
		t.Fatalf("updated app = %+v", updated)
	}
	// Phase 5 widening: pin the round-trip on every widened field so
	// a regression in pgstore/memstore parity trips the coverage gate
	// immediately.
	if updated.RootDir != rootDir || updated.WorkloadName != workloadName || updated.StartCommand != startCmd {
		t.Fatalf("updated workload fields = %+v", updated)
	}
	if _, err := m.UpdateApp(ctx, "missing", UpdateAppParams{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing app update = %v", err)
	}
}

func TestMemStoreCoverageDeploymentsAndBuilds(t *testing.T) {
	m, ctx, _, app, deployment := memCoverageFixture(t)
	if _, err := m.BuildByDeployment(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing build deployment = %v", err)
	}
	build, err := m.CreateBuild(ctx, deployment.ID, DeploymentKindDockerfile, 42, "/tmp/build.log")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.BuildByDeployment(ctx, deployment.ID); err != nil || got.ID != build.ID {
		t.Fatalf("BuildByDeployment = %+v, %v", got, err)
	}
	if _, err := m.ClaimQueuedBuild(ctx, build.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateBuildStatus(ctx, build.ID, BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateBuildStatus(ctx, build.ID, BuildFailed, FailureTimeout, false, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("terminal CAS after success = %v", err)
	}
	if _, err := m.ClaimNextQueuedBuild(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty build queue = %v", err)
	}
	if _, err := m.CreateBuild(ctx, "missing", DeploymentKindImage, 0, ""); err == nil {
		t.Fatal("build for unknown deployment should fail")
	}
	second, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:second"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := m.CreateBuild(ctx, second.ID, DeploymentKindImage, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	m.SetBuildStartedAtForTest(queued.ID, time.Now().Add(-time.Hour))
	count, err := m.SweepStuckRunningBuilds(ctx, time.Now())
	if err != nil || count != 0 {
		t.Fatalf("sweep queued = %d, %v", count, err)
	}
	if _, err := m.ClaimQueuedBuild(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.RequeueBuild(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.BuildByID(ctx, queued.ID); got.Status != BuildQueued {
		t.Fatalf("requeued build = %+v", got)
	}
	if err := m.RequeueBuild(ctx, queued.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("requeue already-queued = %v", err)
	}
	stuck, err := m.CreateBuild(ctx, second.ID, DeploymentKindImage, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimQueuedBuild(ctx, stuck.ID); err != nil {
		t.Fatal(err)
	}
	m.SetBuildStartedAtForTest(stuck.ID, time.Now().Add(-time.Hour))
	count, err = m.SweepStuckRunningBuilds(ctx, time.Now().Add(-time.Minute))
	if err != nil || count != 1 {
		t.Fatalf("sweep running = %d, %v", count, err)
	}
	if err := m.MarkDeploymentSuperseded(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LatestSupersededDeployment(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs", "apps/rootfs", 10); err != nil {
		t.Fatal(err)
	}
	failed, err := m.SetDeploymentFailed(ctx, deployment.ID, "build_failed", "failure")
	if err != nil || failed.ErrorCode != "build_failed" {
		t.Fatalf("failed deployment = %+v, %v", failed, err)
	}
}

func TestMemStoreCoverageDomainsAndCrons(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)
	if _, err := m.DomainByName(ctx, "missing.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing domain = %v", err)
	}
	domain, err := m.CreateCustomDomain(ctx, "coverage.example", app.ID, "token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateCustomDomain(ctx, domain.Domain, app.ID, "token"); err == nil {
		t.Fatal("duplicate domain should fail")
	}
	if err := m.MarkDomainVerified(ctx, domain.Domain); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.DomainByName(ctx, domain.Domain); !got.Verified() {
		t.Fatal("domain should be verified")
	}
	if got, err := m.ListDomainsForAccount(ctx, account.ID); err != nil || len(got) != 1 {
		t.Fatalf("account domains = %+v, %v", got, err)
	}
	if err := m.DeleteCustomDomain(ctx, domain.Domain); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteCustomDomain(ctx, domain.Domain); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat domain delete = %v", err)
	}
	if _, err := m.CreateCron(ctx, "missing", "* * * * *", "/", true); err == nil {
		t.Fatal("cron for unknown app should fail")
	}
	cron, err := m.CreateCron(ctx, app.ID, "* * * * *", "/health", true)
	if err != nil {
		t.Fatal(err)
	}
	path := "/invoke"
	enabled := false
	updated, err := m.UpdateCron(ctx, cron.ID, nil, &path, &enabled, nil)
	if err != nil || updated.Path != path || updated.Enabled {
		t.Fatalf("updated cron = %+v, %v", updated, err)
	}
	fired := time.Now()
	if err := m.MarkCronFired(ctx, cron.ID, fired); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ListCronsForApp(ctx, app.ID); len(got) != 1 || !got[0].LastFiredAt.Equal(fired) {
		t.Fatalf("app crons = %+v", got)
	}
	if got, _ := m.ListEnabledCrons(ctx); len(got) != 0 {
		t.Fatalf("enabled crons = %+v", got)
	}
	if err := m.DeleteCron(ctx, cron.ID, "wrong-app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-app cron delete = %v", err)
	}
	if err := m.DeleteCron(ctx, cron.ID, app.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMemStoreCoverageInstancesSnapshotsAndNodes(t *testing.T) {
	m, ctx, account, app, deployment := memCoverageFixture(t)
	instance, err := m.CreateInstance(ctx, app.ID, deployment.ID, string(StateRunning), 512, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstanceByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing instance = %v", err)
	}
	if err := m.UpdateInstanceStateWithTimestamp(ctx, instance.ID, string(StateSnapshotting), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateInstanceStateToTerminal(ctx, instance.ID, string(StateStopped), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RunningInstanceForApp(ctx, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no running instance = %v", err)
	}
	if n, err := m.TouchInstancesLastSeen(ctx, []InstanceTouch{{InstanceID: instance.ID, LastRequest: time.Now()}, {InstanceID: "missing"}}); err != nil || n != 1 {
		t.Fatalf("touch count = %d, %v", n, err)
	}
	if err := m.SetInstanceMode(ctx, "missing", InstanceModeMirror); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set instance mode missing = %v", err)
	}
	if err := m.SetInstanceMode(ctx, instance.ID, InstanceModeMirror); err != nil {
		t.Fatalf("set instance mode = %v", err)
	}
	requestAt := time.Now()
	if n, err := m.TouchInstancesWithRequestDelta(ctx, []InstanceTouch{
		{InstanceID: instance.ID, LastRequest: requestAt, RequestDelta: 3},
		{InstanceID: "missing", RequestDelta: 99},
	}); err != nil || n != 1 {
		t.Fatalf("touch request delta = %d, %v", n, err)
	}
	updatedInstance, err := m.InstanceByID(ctx, instance.ID)
	if err != nil || updatedInstance.Mode != string(InstanceModeMirror) || updatedInstance.RequestCount != 3 || !updatedInstance.LastRequestAt.Equal(requestAt) {
		t.Fatalf("updated instance = %+v, %v", updatedInstance, err)
	}
	if err := m.DeleteInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteInstance(ctx, instance.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat instance delete = %v", err)
	}
	if _, err := m.CreateSnapshot(ctx, Snapshot{DeploymentID: deployment.ID}); err == nil {
		t.Fatal("snapshot without storage key should fail")
	}
	snap, err := m.CreateSnapshot(ctx, Snapshot{DeploymentID: deployment.ID, FCVersion: "v1", StorageKey: "snap/1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSnapshot(ctx, Snapshot{DeploymentID: deployment.ID, FCVersion: "v1", StorageKey: "snap/1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate snapshot = %v", err)
	}
	if err := m.MarkSnapshotStale(ctx, snap.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LatestSnapshot(ctx, deployment.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale latest snapshot = %v", err)
	}
	node, err := m.CreateComputeNode(ctx, ComputeNode{Name: "coverage-node", TargetURL: "unix:///run/vmmd.sock", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateComputeNode(ctx, ComputeNode{Name: node.Name}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate node = %v", err)
	}
	before, err := m.ListComputeNodes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkComputeNodeInactive(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	after, err := m.ListComputeNodes(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)-1 {
		t.Fatalf("inactive filtered one node: before=%d after=%d", len(before), len(after))
	}
	if err := m.SetComputeNodeActive(ctx, node.ID, true); err != nil {
		t.Fatal(err)
	}
	up, err := m.UpsertComputeNode(ctx, ComputeNode{Name: node.Name, TargetURL: "unix:///new.sock", VPCPUs: 4})
	if err != nil || up.ID != node.ID || up.VPCPUs != 4 || !up.Active {
		t.Fatalf("upsert node = %+v, %v", up, err)
	}
	if err := m.DeleteComputeNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteComputeNode(ctx, node.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat node delete = %v", err)
	}
	if _, err := m.ListLatestInstancePerApp(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMemStoreCoverageAuthUsageAndEvents(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)
	hash := []byte("login-token")
	if err := m.IssueLoginToken(ctx, hash, account.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ConsumeLoginToken(ctx, hash); err != nil || got != account.ID {
		t.Fatalf("consume login token = %q, %v", got, err)
	}
	if _, err := m.ConsumeLoginToken(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay login token = %v", err)
	}
	expired := []byte("expired-token")
	if err := m.IssueLoginToken(ctx, expired, account.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConsumeLoginToken(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired login token = %v", err)
	}
	code := []byte("cli-code")
	if err := m.IssueCliAuthCode(ctx, code, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if status, _, err := m.ConsumeCliAuthCode(ctx, code); err != nil || status != api.CliAuthStatusPending {
		t.Fatalf("pending cli code = %v, %v", status, err)
	}
	if err := m.ClaimCliAuthCode(ctx, code, account.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ClaimCliAuthCode(ctx, code, account.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeat cli claim = %v", err)
	}
	if status, got, err := m.ConsumeCliAuthCode(ctx, code); err != nil || status != api.CliAuthStatusConsumed || got != account.ID {
		t.Fatalf("consume cli code = %v, %q, %v", status, got, err)
	}
	if _, _, err := m.ConsumeCliAuthCode(ctx, code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay cli code = %v", err)
	}
	if err := m.SetAccountPassword(ctx, account.ID, "phc"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AccountPasswordByAccountID(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.SetAccountPassword(ctx, "", "phc"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty password account = %v", err)
	}
	if err := m.UpsertOAuthLink(ctx, account.ID, "google", "subject", account.Email, true); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertOAuthLink(ctx, "other", "google", "subject", "other@example.com", true); !errors.Is(err, ErrConflict) {
		t.Fatalf("oauth takeover = %v", err)
	}
	minute := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	if err := m.AppendUsage(ctx, account.ID, app.ID, "instance", minute, 100, 2, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendUsage(ctx, account.ID, app.ID, "instance", minute.Add(30*time.Second), 999, 999, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	usage, err := m.UsageByMonth(ctx, account.ID, minute)
	if err != nil || len(usage) != 1 || usage[0].MBSeconds != 100 || usage[0].Requests != 2 {
		t.Fatalf("usage month = %+v, %v", usage, err)
	}
	if ok, _ := m.HasStripePushHour(ctx, account.ID, minute); ok {
		t.Fatal("stripe hour unexpectedly recorded")
	}
	if err := m.RecordStripePushHour(ctx, account.ID, minute); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.HasStripePushHour(ctx, account.ID, minute); !ok {
		t.Fatal("stripe hour not recorded")
	}
	if err := m.AppendEvent(ctx, "tester", "coverage", nil, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListEvents(ctx, "not-a-uuid", 10); err != nil || len(got) != 0 {
		t.Fatalf("invalid event filter = %+v, %v", got, err)
	}
}

// TestMemStoreCoverageMailSuppressions exercises MemStore parity
// for the suppression list (issue #246 acceptance item 7).
// Pin every clause the PgStore implementation honours so a
// regression in pgstore/memstore parity trips the coverage gate
// immediately.
func TestMemStoreCoverageMailSuppressions(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()

	// Validation: empty inputs are rejected on both stores the
	// same way — the apid daemon's bounce handler relies on
	// these errors surfacing as ErrInvalidArgument, not silently
	// being treated as "inserted".
	if _, err := m.RecordMailSuppression(ctx, MailSuppressionInput{}); err == nil {
		t.Fatal("empty RecordMailSuppression should fail")
	}

	// Fresh insert + replay dedupe on (source, provider_event_id).
	// Both events came from the same Resend event ID so the second
	// call must return inserted=false without mutating state.
	evID := "evt_" + uuid.NewString()
	in := MailSuppressionInput{
		Email:           "Alice@Example.com",
		Reason:          MailSuppressionHardBounce,
		Source:          MailSuppressionSourceResend,
		ProviderEventID: evID,
	}
	inserted, err := m.RecordMailSuppression(ctx, in)
	if err != nil || !inserted {
		t.Fatalf("first RecordMailSuppression = %v, %v", inserted, err)
	}
	inserted, err = m.RecordMailSuppression(ctx, in)
	if err != nil || inserted {
		t.Fatalf("replay RecordMailSuppression = %v, %v", inserted, err)
	}

	// Case-insensitive lookup: the row was stored under the
	// lower-cased key but IsMailSuppressed must match any case
	// the customer types in their address field.
	if ok, err := m.IsMailSuppressed(ctx, "alice@example.com"); err != nil || !ok {
		t.Fatalf("lower lookup = %v, %v", ok, err)
	}
	if ok, err := m.IsMailSuppressed(ctx, "ALICE@EXAMPLE.COM"); err != nil || !ok {
		t.Fatalf("upper lookup = %v, %v", ok, err)
	}
	if ok, err := m.IsMailSuppressed(ctx, "AlIcE@eXaMpLe.cOm"); err != nil || !ok {
		t.Fatalf("mixed lookup = %v, %v", ok, err)
	}

	// Different address (same row otherwise) must NOT match.
	if ok, err := m.IsMailSuppressed(ctx, "bob@example.com"); err != nil || ok {
		t.Fatalf("different address = %v, %v", ok, err)
	}

	// Expired row must fall out of the active set so a TTL'd
	// suppression does not block future mail.
	expired := MailSuppressionInput{
		Email:           "expired@example.com",
		Reason:          MailSuppressionComplaint,
		Source:          MailSuppressionSourcePostmark,
		ProviderEventID: "evt_" + uuid.NewString(),
		ExpiresAt:       timePtr(time.Now().Add(-time.Minute)),
	}
	if _, err := m.RecordMailSuppression(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.IsMailSuppressed(ctx, "expired@example.com"); err != nil || ok {
		t.Fatalf("expired still active = %v, %v", ok, err)
	}

	// Future expiry: row counts as active (within TTL).
	future := MailSuppressionInput{
		Email:           "future@example.com",
		Reason:          MailSuppressionManual,
		Source:          MailSuppressionSourceOperator,
		ProviderEventID: "evt_" + uuid.NewString(),
		ExpiresAt:       timePtr(time.Now().Add(time.Hour)),
	}
	if _, err := m.RecordMailSuppression(ctx, future); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.IsMailSuppressed(ctx, "future@example.com"); err != nil || !ok {
		t.Fatalf("future expiry not active = %v, %v", ok, err)
	}

	// Empty email rejected.
	if _, err := m.IsMailSuppressed(ctx, ""); err == nil {
		t.Fatal("empty IsMailSuppressed should fail")
	}
}

// timePtr is a tiny helper for the coverage test that keeps the
// input declarations readable.
func timePtr(t time.Time) *time.Time { return &t }
