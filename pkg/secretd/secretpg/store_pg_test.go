//go:build !no_pg

// adr: 20261110210000
package secretpg_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/secretd/secretpg"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStoreOnPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	st := state.NewPgStore(pool)
	store := secretpg.New(pool, st)
	ctx := t.Context()

	node, err := st.CreateComputeNode(ctx, state.ComputeNode{
		Name: "secretd-" + uuid.NewString()[:8], TargetURL: "unix:///run/faas/vmmd.sock",
		VPCPUs: 4, VCPUBudget: 160, MemMB: 8192, AdmissionCeilingMB: 8192, MaxConcurrency: 16, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := st.CreateComputeNode(ctx, state.ComputeNode{
		Name: "secretd-peer-" + uuid.NewString()[:8], TargetURL: "unix:///run/faas/vmmd.sock",
		VPCPUs: 4, VCPUBudget: 160, MemMB: 8192, AdmissionCeilingMB: 8192, MaxConcurrency: 16, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := st.CreateAccount(ctx, uuid.NewString()+"@secretd.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "secretd-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAppNodeID(ctx, app.ID, node.ID); err != nil {
		t.Fatal(err)
	}

	// Node names resolve to active nodes only.
	if id, err := store.NodeIDByName(ctx, node.Name); err != nil || id != node.ID {
		t.Fatalf("node by name = %q, %v", id, err)
	}
	if _, err := store.NodeIDByName(ctx, "no-such-node"); !errors.Is(err, secretd.ErrUnknownNode) {
		t.Fatalf("unknown node = %v", err)
	}
	if err := st.SetComputeNodeActive(ctx, peer.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NodeIDByName(ctx, peer.Name); !errors.Is(err, secretd.ErrUnknownNode) {
		t.Fatalf("inactive node = %v", err)
	}

	// The app's owner and resource account.
	target, err := store.AppTarget(ctx, app.ID)
	if err != nil || target.OwnerNodeID != node.ID || target.AccountID != account.ID {
		t.Fatalf("app target = %+v, %v", target, err)
	}

	// Grants: valid until expiry, renewed by upsert, swept after.
	now := time.Now().UTC().Truncate(time.Microsecond)
	expires, err := store.UpsertGrant(ctx, app.ID, peer.ID, node.ID, now.Add(10*time.Minute))
	if err != nil || !expires.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("upsert grant = %v, %v", expires, err)
	}
	if ok, err := store.GrantValid(ctx, app.ID, peer.ID, now); err != nil || !ok {
		t.Fatalf("grant valid now = %v, %v", ok, err)
	}
	if ok, _ := store.GrantValid(ctx, app.ID, peer.ID, now.Add(11*time.Minute)); ok {
		t.Fatal("grant valid after expiry")
	}
	if ok, _ := store.GrantValid(ctx, app.ID, node.ID, now); ok {
		t.Fatal("a grant for the peer is valid for another node")
	}
	if _, err := store.UpsertGrant(ctx, app.ID, peer.ID, node.ID, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.GrantValid(ctx, app.ID, peer.ID, now.Add(20*time.Minute)); !ok {
		t.Fatal("renewed grant not extended")
	}
	if n, err := store.DeleteExpiredGrants(ctx, now.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("expired sweep = %d, %v", n, err)
	}

	// Audit rows land.
	if err := store.RecordOpen(ctx, node.ID, app.ID, "owner", 3); err != nil {
		t.Fatal(err)
	}
	var via string
	var entries int
	if err := pool.QueryRow(ctx, `select via, entries from secret_open_audit where app_id = $1`, app.ID).Scan(&via, &entries); err != nil || via != "owner" || entries != 3 {
		t.Fatalf("audit = %s %d %v", via, entries, err)
	}
	if err := store.RecordOpen(ctx, node.ID, app.ID, "stolen", 1); err == nil {
		t.Fatal("audit accepted an unknown via")
	}
}

// Slice 3d: the by-reference lookups read only the named app's rows.
func TestReferenceLookupsOnPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	st := state.NewPgStore(pool)
	store := secretpg.New(pool, st)
	ctx := t.Context()

	account, err := st.CreateAccount(ctx, uuid.NewString()+"@secretd.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func() state.App {
		app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "ref-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	appA, appB := newApp(), newApp()

	// App secrets: the app's own (scope, key) row only.
	if err := st.UpsertAppSecret(ctx, account.ID, appA.ID, "DB_URL", []byte("ct-a")); err != nil {
		t.Fatal(err)
	}
	if ct, err := store.AppSecretCiphertext(ctx, appA.ID, "default", "DB_URL"); err != nil || string(ct) != "ct-a" {
		t.Fatalf("own secret = %q, %v", ct, err)
	}
	if _, err := store.AppSecretCiphertext(ctx, appB.ID, "default", "DB_URL"); !errors.Is(err, secretd.ErrUnknownSecret) {
		t.Fatalf("another app's secret = %v", err)
	}
	if _, err := store.AppSecretCiphertext(ctx, appA.ID, "preview", "DB_URL"); !errors.Is(err, secretd.ErrUnknownSecret) {
		t.Fatalf("other scope = %v", err)
	}

	// Sidecar env: only from a deployment of the named app.
	sidecars := []byte(`[{"name":"proxy","image":"example/proxy","env":{"TOKEN":"` + base64.StdEncoding.EncodeToString([]byte("ct-token")) + `"}}]`)
	dep, err := st.CreateDeployment(ctx, state.Deployment{AppID: appA.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:secretd-ref", Status: state.DeployLive, Sidecars: sidecars})
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.SidecarEnv(ctx, appA.ID, dep.ID, "proxy")
	if err != nil || string(env["TOKEN"]) != "ct-token" {
		t.Fatalf("own sidecar env = %q, %v", env, err)
	}
	if _, err := store.SidecarEnv(ctx, appB.ID, dep.ID, "proxy"); !errors.Is(err, secretd.ErrUnknownSidecar) {
		t.Fatalf("another app's deployment = %v", err)
	}
	if _, err := store.SidecarEnv(ctx, appA.ID, uuid.NewString(), "proxy"); !errors.Is(err, secretd.ErrUnknownSidecar) {
		t.Fatalf("unknown deployment = %v", err)
	}

	// Log drains (slice 3f): a drain of the named app, with a header.
	drain, err := st.CreateAppLogDrain(ctx, state.AppLogDrain{AppID: appA.ID, AccountID: account.ID,
		Kind: state.AppLogDrainKindHTTPJSON, TargetURL: "https://logs.example.com/in", AuthHeaderSealed: []byte("sealed-header"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if sealed, err := store.LogDrainHeader(ctx, appA.ID, drain.ID); err != nil || string(sealed) != "sealed-header" {
		t.Fatalf("own drain header = %q, %v", sealed, err)
	}
	if _, err := store.LogDrainHeader(ctx, appB.ID, drain.ID); !errors.Is(err, secretd.ErrUnknownDrain) {
		t.Fatalf("another app's drain = %v", err)
	}
	if _, err := store.LogDrainHeader(ctx, appA.ID, "not-a-uuid"); !errors.Is(err, secretd.ErrUnknownDrain) {
		t.Fatalf("bad drain id = %v", err)
	}

	// Crash captures: a live sealed capture's app and key. The fixture row
	// skips foreign keys and the status-transition trigger.
	captureID := uuid.NewString()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO crash_captures
		(id, account_id, app_id, deployment_id, instance_id, trigger, status, requested_at, updated_at,
		 storage_key, vmstate_storage_key, fc_version, mem_bytes, captured_at, expires_at,
		 plaintext_state, sealed_key, encrypted_at)
		VALUES ($1, $2, $3, $4, $5, 'manual', 'ready', now(), now(),
		 'k/mem', 'k/vmstate', '1.12.0', 1, now(), now() + interval '1 day',
		 'sealed', '\x01'::bytea, now())`, captureID, account.ID, appA.ID, dep.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	// Triggers (slice 3g): a trigger of the named app only.
	triggerID := uuid.NewString()
	if _, err := conn.Exec(ctx, `INSERT INTO triggers (id, account_id, app_id, kind, slug, config)
		VALUES ($1, $2, $3, 'kafka', 'orders', '{"topic":"orders"}'::jsonb)`, triggerID, account.ID, appA.ID); err != nil {
		t.Fatal(err)
	}
	// Edge rule header values (slice 3i): a value of the named app that one
	// of its headers rules references; an orphaned value does not open.
	referenced, orphan := uuid.NewString(), uuid.NewString()
	if _, err := conn.Exec(ctx, `INSERT INTO edge_rule_header_secrets (id, account_id, app_id, ciphertext)
		VALUES ($1, $3, $4, '\x02'::bytea), ($2, $3, $4, '\x03'::bytea)`, referenced, orphan, account.ID, appA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO edge_rules (account_id, app_id, match_host, kind, action)
		VALUES ($1, $2, 'a.example.com', 'headers', jsonb_build_object('kind', 'headers', 'headers',
		  jsonb_build_object('request_headers', jsonb_build_array(
		    jsonb_build_object('name', 'Authorization', 'action', 'set', 'secret', true, 'secret_id', $3::text)))))`,
		account.ID, appA.ID, referenced); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `RESET session_replication_role`); err != nil {
		t.Fatal(err)
	}
	if sealed, err := store.EdgeHeaderSecret(ctx, appA.ID, referenced); err != nil || len(sealed) != 1 || sealed[0] != 2 {
		t.Fatalf("referenced edge header value = %x, %v", sealed, err)
	}
	for name, args := range map[string][2]string{
		"orphaned value":      {appA.ID, orphan},
		"another app's value": {appB.ID, referenced},
		"bad value id":        {appA.ID, "not-a-uuid"},
	} {
		if _, err := store.EdgeHeaderSecret(ctx, args[0], args[1]); !errors.Is(err, secretd.ErrUnknownEdgeHeaderSecret) {
			t.Errorf("%s = %v", name, err)
		}
	}
	if kind, cfg, err := store.TriggerConfig(ctx, appA.ID, triggerID); err != nil || kind != "kafka" || !strings.Contains(string(cfg), "orders") {
		t.Fatalf("own trigger = %s %s %v", kind, cfg, err)
	}
	if _, _, err := store.TriggerConfig(ctx, appB.ID, triggerID); !errors.Is(err, secretd.ErrUnknownTrigger) {
		t.Fatalf("another app's trigger = %v", err)
	}
	if app, sealed, err := store.CaptureKey(ctx, captureID); err != nil || app != appA.ID || len(sealed) != 1 {
		t.Fatalf("capture key = %s %x %v", app, sealed, err)
	}
	if _, _, err := store.CaptureKey(ctx, uuid.NewString()); !errors.Is(err, secretd.ErrUnknownCapture) {
		t.Fatalf("unknown capture = %v", err)
	}
	if _, _, err := store.CaptureKey(ctx, "not-a-uuid"); !errors.Is(err, secretd.ErrUnknownCapture) {
		t.Fatalf("bad capture id = %v", err)
	}
}
