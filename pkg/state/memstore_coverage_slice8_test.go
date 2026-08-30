package state

import (
	"encoding/json"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// This slice closes the remaining <100% MemStore functions that carry
// real branching (list aggregation, cursor/limit semantics, alert-rule
// update params) plus the last pure helpers.

func TestMemStoreCoverageUsageByAccount(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)
	minute := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	// Rows with distinct (instance, minute) keys aggregate by (app,
	// month). Same (instance, minute) would be first-write-wins for
	// mb_seconds/requests — use different instances.
	if err := m.AppendUsage(ctx, account.ID, app.ID, "inst-1", minute, 100, 2, 5, 6, 7, 8, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendUsage(ctx, account.ID, app.ID, "inst-2", minute, 50, 1, 10, 11, 12, 13, 0, 0); err != nil {
		t.Fatal(err)
	}
	otherMonth := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	if err := m.AppendUsage(ctx, account.ID, app.ID, "inst-1", otherMonth, 999, 9, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	// Full scan → 2 buckets.
	got, err := m.UsageByAccount(ctx, account.ID, time.Time{})
	if err != nil || len(got) != 2 {
		t.Fatalf("usage by account = %+v, %v", got, err)
	}
	// Since filter excludes the June row.
	got, err = m.UsageByAccount(ctx, account.ID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(got) != 1 {
		t.Fatalf("usage since = %+v, %v", got, err)
	}
	// Aggregation: the July bucket sums 150 MB, 3 req, 15 cpu, 17 tx,
	// 19 net tx. Sorted by (appID, month), so the first is July.
	if got[0].MBSeconds != 150 || got[0].Requests != 3 || got[0].CPUUsec != 15 || got[0].TXBytes != 17 || got[0].NetTxBytes != 19 {
		t.Fatalf("usage agg = %+v", got[0])
	}
	// Wrong account → empty.
	if got, err := m.UsageByAccount(ctx, "missing", time.Time{}); err != nil || len(got) != 0 {
		t.Fatalf("usage missing = %+v, %v", got, err)
	}
}

func TestMemStoreCoverageListDeployments(t *testing.T) {
	m, ctx, account, app, deployment := memCoverageSlice4Fixture(t)
	// The fixture already created one deployment; add two more with
	// distinct CreatedAt so ordering is deterministic.
	second, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:second", CreatedAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	third, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:third", CreatedAt: time.Now().Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_ = deployment
	// ListDeploymentsForApp — no cap (limit<=0), offset, negative offset.
	if got, err := m.ListDeploymentsForApp(ctx, app.ID, 0, 0); err != nil || len(got) != 3 {
		t.Fatalf("list all = %d, %v", len(got), err)
	}
	if got, err := m.ListDeploymentsForApp(ctx, app.ID, 1, 0); err != nil || len(got) != 1 || got[0].ID != third.ID {
		t.Fatalf("list limit 1 = %+v, %v", got, err)
	}
	if got, err := m.ListDeploymentsForApp(ctx, app.ID, 0, 2); err != nil || len(got) != 1 || got[0].ID != deployment.ID {
		t.Fatalf("list offset 2 = %+v, %v", got, err)
	}
	if got, err := m.ListDeploymentsForApp(ctx, app.ID, 0, -1); err != nil || len(got) != 3 {
		t.Fatalf("list negative offset = %d, %v", len(got), err)
	}
	// Offset beyond the set → empty.
	if got, err := m.ListDeploymentsForApp(ctx, app.ID, 0, 99); err != nil || len(got) != 0 {
		t.Fatalf("list offset beyond = %+v, %v", got, err)
	}
	// ListDeploymentsForAccount — before cursor filters strictly-older.
	if got, err := m.ListDeploymentsForAccount(ctx, account.ID, time.Time{}, 0); err != nil || len(got) != 3 {
		t.Fatalf("account deployments = %d, %v", len(got), err)
	}
	// before = second's CreatedAt → only rows strictly older (the
	// fixture deployment created first).
	if got, err := m.ListDeploymentsForAccount(ctx, account.ID, second.CreatedAt, 0); err != nil || len(got) != 1 || got[0].ID != deployment.ID {
		t.Fatalf("account before = %+v, %v", got, err)
	}
	// limit clamps.
	if got, err := m.ListDeploymentsForAccount(ctx, account.ID, time.Time{}, 2); err != nil || len(got) != 2 {
		t.Fatalf("account limit = %d, %v", len(got), err)
	}
}

func TestMemStoreCoverageListBuildsAndCronsForAccount(t *testing.T) {
	m, ctx, account, app, deployment := memCoverageSlice4Fixture(t)
	// The slice4 fixture already created one build on the deployment;
	// add a second so the list returns both.
	if _, err := m.CreateBuild(ctx, deployment.ID, DeploymentKindImage, 10, "/tmp/acc.log"); err != nil {
		t.Fatal(err)
	}
	builds, err := m.ListBuildsForAccount(ctx, account.ID)
	if err != nil || len(builds) != 2 {
		t.Fatalf("builds for account = %+v, %v", builds, err)
	}
	if got, err := m.ListBuildsForAccount(ctx, "missing"); err != nil || len(got) != 0 {
		t.Fatalf("builds missing = %+v, %v", got, err)
	}
	// Crons on the fixture app.
	cron, err := m.CreateCron(ctx, app.ID, "* * * * *", "/health", true)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListCronsForAccount(ctx, account.ID); err != nil || len(got) != 1 || got[0].ID != cron.ID {
		t.Fatalf("crons for account = %+v, %v", got, err)
	}
	if got, err := m.ListCronsForAccount(ctx, "missing"); err != nil || len(got) != 0 {
		t.Fatalf("crons missing = %+v, %v", got, err)
	}
}

func TestMemStoreCoverageAppsForProject(t *testing.T) {
	m, ctx, account, _, _ := memCoverageSlice4Fixture(t)
	// Create the project + apps in one ApplyProjectPlan call (no
	// pre-create, so the slug doesn't collide).
	proj, apps, _, err := m.ApplyProjectPlan(ctx, Project{AccountID: account.ID, Slug: "proj-apps"}, []App{{Slug: "worker-a", WorkloadName: "worker-a", RAMMB: 128, Status: AppActive}}, nil, api.Limits{DeployedApps: 10})
	if err != nil {
		t.Fatal(err)
	}
	_ = apps
	// AppsForProject — hit + wrong-account + missing project.
	if got, err := m.AppsForProject(ctx, account.ID, proj.ID); err != nil || len(got) != 1 || got[0].Slug != "worker-a" {
		t.Fatalf("apps for project = %+v, %v", got, err)
	}
	if _, err := m.AppsForProject(ctx, "other-account", proj.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("apps for project wrong account = %v", err)
	}
	if _, err := m.AppsForProject(ctx, account.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("apps for project missing = %v", err)
	}
}

func TestMemStoreCoverageAlertRuleUpdate(t *testing.T) {
	m, ctx, account, _, _ := memCoverageFixture(t)
	rule, err := m.CreateAlertRule(ctx, AlertRule{AccountID: account.ID, Name: "mem-high"})
	if err != nil {
		t.Fatal(err)
	}
	name := "mem-high-v2"
	enabled := false
	metric := AlertMetric("cpu_usage")
	comparison := AlertComparison("gt")
	threshold := 90.0
	windowSpec := AlertWindowSpec("5m")
	webhookURL := "https://hooks.example.com/x"
	secret := []byte("sealed")
	cooldown := 10
	updated, err := m.UpdateAlertRule(ctx, rule.ID, UpdateAlertRuleParams{
		Name: &name, Enabled: &enabled, Metric: &metric, Comparison: &comparison,
		Threshold: &threshold, WindowSpec: &windowSpec, WebhookURL: &webhookURL,
		WebhookSecretSealed: &secret, CooldownMinutes: &cooldown,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || updated.Enabled || updated.Threshold != 90.0 || updated.WebhookURL != webhookURL ||
		string(updated.WebhookSecretSealed) != "sealed" || updated.CooldownMinutes != cooldown {
		t.Fatalf("updated alert = %+v", updated)
	}
	// Nil params leave fields untouched.
	updated, err = m.UpdateAlertRule(ctx, rule.ID, UpdateAlertRuleParams{})
	if err != nil || updated.Name != name {
		t.Fatalf("nil-param update = %+v, %v", updated, err)
	}
	if _, err := m.UpdateAlertRule(ctx, "missing", UpdateAlertRuleParams{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing = %v", err)
	}
	// DeleteAlertRule + ListAlertDeliveriesForRule limit clamp.
	if err := m.DeleteAlertRule(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteAlertRule(ctx, rule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing = %v", err)
	}
}

func TestMemStoreCoverageAlertDeliveries(t *testing.T) {
	m, ctx, account, _, _ := memCoverageFixture(t)
	rule, err := m.CreateAlertRule(ctx, AlertRule{AccountID: account.ID, Name: "delivery-rule", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	// ClaimAlertFire — win + duplicate-loss + stale (at < last_fired).
	id1, won, err := m.ClaimAlertFire(ctx, rule.ID, "bucket-1", nil, 1.0, at)
	if err != nil || !won || id1 == "" {
		t.Fatalf("claim fire 1 = %q/%v, %v", id1, won, err)
	}
	if id2, won, err := m.ClaimAlertFire(ctx, rule.ID, "bucket-1", nil, 1.0, at.Add(time.Minute)); err != nil || won || id2 != "" {
		t.Fatalf("claim fire duplicate = %q/%v, %v", id2, won, err)
	}
	// Missing rule → ErrNotFound.
	if _, _, err := m.ClaimAlertFire(ctx, "missing", "k", nil, 1.0, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim fire missing rule = %v", err)
	}
	// New bucket with at earlier than last_fired → lose.
	if _, won, err := m.ClaimAlertFire(ctx, rule.ID, "bucket-2", nil, 1.0, at.Add(-time.Hour)); err != nil || won {
		t.Fatalf("claim fire stale = %v, %v", won, err)
	}
	// ListAlertDeliveriesForRule — one row from the winning claim.
	deliveries, err := m.ListAlertDeliveriesForRule(ctx, rule.ID, 0, false)
	if err != nil || len(deliveries) != 1 || deliveries[0].Payload == nil {
		t.Fatalf("deliveries = %+v, %v", deliveries, err)
	}
	if got, err := m.ListAlertDeliveriesForRule(ctx, "missing", 10, false); err != nil || len(got) != 0 {
		t.Fatalf("deliveries missing = %+v, %v", got, err)
	}
}

func TestMemStoreCoverageListOrphanedApps(t *testing.T) {
	m, ctx, _, app, _ := memCoverageSlice4Fixture(t)
	// Create a node and mark it inactive; the fixture app has no owner
	// node, so claim it first, then reassign it to the dead node.
	node, err := m.CreateComputeNode(ctx, ComputeNode{Name: "dead-node", TargetURL: "unix:///run/vmmd.sock", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkComputeNodeInactive(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.SetAppNodeID(ctx, app.ID, DefaultLocalNodeName); err != nil {
		t.Fatal(err)
	}
	if err := m.ReassignAppOwner(ctx, app.ID, DefaultLocalNodeName, node.ID); err != nil {
		t.Fatal(err)
	}
	// Cooldown: the app was just reassigned, so with a large cooldown it
	// is excluded; with zero cooldown it appears.
	if got, err := m.ListOrphanedApps(ctx, 3600, 10); err != nil || len(got) != 0 {
		t.Fatalf("orphaned with cooldown = %+v, %v", got, err)
	}
	if got, err := m.ListOrphanedApps(ctx, 0, 10); err != nil || len(got) != 1 || got[0].ID != app.ID {
		t.Fatalf("orphaned no cooldown = %+v, %v", got, err)
	}
}

func TestMemStoreCoverageMiscHelpers(t *testing.T) {
	// derefPrefixes.
	if derefPrefixes(nil) != nil {
		t.Fatal("derefPrefixes(nil) should be nil")
	}
	prefs := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	if got := derefPrefixes(&prefs); len(got) != 1 {
		t.Fatalf("derefPrefixes = %+v", got)
	}
	// intOrZero.
	if intOrZero(nil) != 0 {
		t.Fatal("intOrZero(nil) should be 0")
	}
	v := 7
	if intOrZero(&v) != 7 {
		t.Fatal("intOrZero(&7) should be 7")
	}
	// ScalingPolicy.UnmarshalJSON malformed-input branch (the happy path
	// is covered in slice7; this closes the error return).
	var p ScalingPolicy
	if err := json.Unmarshal([]byte(`{"min_instances":`), &p); err == nil {
		t.Fatal("truncated policy should error")
	}
}
