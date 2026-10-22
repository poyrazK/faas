package trafficguard_test

import (
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func TestPgMCPGatewayPromotion(t *testing.T) {
	s, pool, ctx := openMCPAccessStore(t)
	account, app, stable := seedMCPAccessLive(t, s, ctx, "gateway-promotion")
	candidate, err := s.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: strings.Repeat("d", 64), Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	// A non-overlapping rule produces an account revision without changing the MCP fingerprint.
	_, err = pool.Exec(ctx, `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,kind,action) VALUES($1,$2,'unrelated.example','/**','jwt','{"kind":"jwt","jwt":{"issuer":"https://issuer.example","audience":["mcp"],"jwks_url":"https://issuer.example/jwks"}}')`, account, app)
	if err != nil {
		t.Fatal(err)
	}
	position, err := s.LatestAccountEdgeRulePolicyPosition(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	expected := pgMCPAccessExpectation(t, s, ctx, account)
	revision := position.DesiredRevision
	expected.AccountRevision = &revision
	recordCount := 0
	promote := func(want error) {
		t.Helper()
		_, err := s.UpdateDeploymentTraffic(state.WithMCPAccessPolicyExpectation(ctx, account, app, expected), candidate.ID, 100, stable)
		if !errors.Is(err, want) {
			t.Fatalf("got %v want %v", err, want)
		}
		rows, listErr := s.ListDeploymentAudit(ctx, candidate.ID, 100)
		if listErr != nil {
			t.Fatal(listErr)
		}
		recordCount++
		if len(rows) != recordCount {
			t.Fatalf("records %d want %d", len(rows), recordCount)
		}
		var data struct {
			Evidence api.MCPAdmissionEvidence `json:"mcp_admission"`
		}
		if err := json.Unmarshal(rows[0].Data, &data); err != nil {
			t.Fatal(err)
		}
		outcome := "rejected"
		if want == nil {
			outcome = "accepted"
		}
		if rows[0].Kind != state.DeployMCPAdmission || data.Evidence.Outcome != outcome || data.Evidence.ExpectedRevision == nil || *data.Evidence.ExpectedRevision != revision || data.Evidence.CapturedAt.IsZero() || data.Evidence.ID == "" {
			t.Fatalf("invalid admission record: %+v", data)
		}
		if strings.Contains(string(rows[0].Data), "issuer.example") || strings.Contains(string(rows[0].Data), "jwks") {
			t.Fatal("private JWT action in evidence")
		}
		if want != nil {
			d, e := s.GetDeploymentByIDScopedToSuperseded(ctx, app, candidate.ID)
			if e != nil || d.TrafficPercent != 0 {
				t.Fatalf("rejected promotion changed traffic: %+v %v", d, e)
			}
		}
	}
	promote(state.ErrMCPGatewayEvidenceUnavailable)
	_, err = pool.Exec(ctx, `INSERT INTO compute_nodes(name,target_url,gateway_target_url,role,lifecycle,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb) VALUES('mcp-gateway','unix:///run/test.sock','tcp://127.0.0.1:8080','compute-node','active',2,1024,10,512)`)
	if err != nil {
		t.Fatal(err)
	}
	promote(state.ErrMCPGatewayEvidenceStale)
	if err = s.UpsertGatewayEdgeRuleWatermark(ctx, "mcp-gateway", "00000000-0000-0000-0000-000000000001", revision); err != nil {
		t.Fatal(err)
	}
	// Restart after a successful CLI verification invalidates its old replay position.
	if err = s.UpsertGatewayEdgeRuleWatermark(ctx, "mcp-gateway", "00000000-0000-0000-0000-000000000002", 0); err != nil {
		t.Fatal(err)
	}
	promote(state.ErrMCPGatewayReplayPending)
	if err = s.UpsertGatewayEdgeRuleWatermark(ctx, "mcp-gateway", "00000000-0000-0000-0000-000000000002", revision); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE gateway_edge_rule_watermarks SET observed_at=clock_timestamp()-interval '11 seconds'`)
	if err != nil {
		t.Fatal(err)
	}
	promote(state.ErrMCPGatewayEvidenceStale)
	if err = s.UpsertGatewayEdgeRuleWatermark(ctx, "mcp-gateway", "00000000-0000-0000-0000-000000000002", revision); err != nil {
		t.Fatal(err)
	}
	// A new member is visible even if all previously audited members converged.
	_, err = pool.Exec(ctx, `INSERT INTO compute_nodes(name,target_url,gateway_target_url,role,lifecycle,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb) VALUES('mcp-new','unix:///run/new.sock','tcp://127.0.0.1:8081','compute-node','active',2,1024,10,512)`)
	if err != nil {
		t.Fatal(err)
	}
	promote(state.ErrMCPGatewayEvidenceStale)
	if err = s.UpsertGatewayEdgeRuleWatermark(ctx, "mcp-new", "00000000-0000-0000-0000-000000000003", revision); err != nil {
		t.Fatal(err)
	}
	// Hold a real policy transaction open while promotion starts. The traffic
	// transaction must wait, then reject the newly committed account revision.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE edge_rules SET priority=priority+1 WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('mcp_access_policy'), hashtext($1))`, account); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.UpdateDeploymentTraffic(state.WithMCPAccessPolicyExpectation(ctx, account, app, expected), candidate.ID, 100, stable)
		done <- err
	}()
	waitMCPTransactionBlockedBy(t, pool, tx.Conn().PgConn().PID())
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, state.ErrMCPAccountRevisionChanged) {
			t.Fatalf("concurrent revision change: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	recordCount++ // The directly invoked concurrent rejection is retained too.
	deployment, err := s.DeploymentByID(ctx, candidate.ID)
	if err != nil || deployment.TrafficPercent != 0 {
		t.Fatalf("concurrent rejection changed traffic: %+v %v", deployment, err)
	}
	position, err = s.LatestAccountEdgeRulePolicyPosition(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	revision = position.DesiredRevision
	promote(state.ErrMCPGatewayReplayPending)
	for _, node := range []string{"mcp-gateway", "mcp-new"} {
		if err = s.UpsertGatewayEdgeRuleWatermark(ctx, node, "00000000-0000-0000-0000-000000000004", revision); err != nil {
			t.Fatal(err)
		}
	}
	promote(nil)
	rows, err := s.ListDeploymentAudit(ctx, candidate.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var accepted struct {
		Evidence api.MCPAdmissionEvidence `json:"mcp_admission"`
	}
	if err = json.Unmarshal(rows[0].Data, &accepted); err != nil {
		t.Fatal(err)
	}
	if len(accepted.Evidence.Gateways) != 2 || accepted.Evidence.Gateways[0].BootID == nil || accepted.Evidence.ObservedRevision == nil || *accepted.Evidence.ObservedRevision != revision {
		t.Fatalf("gateway witnesses missing: %+v", accepted)
	}
	// A duplicate decision id cannot overwrite a committed acceptance.
	duplicate := accepted
	duplicate.Evidence.Outcome = "rejected"
	raw, _ := json.Marshal(map[string]any{"mcp_admission": duplicate.Evidence})
	if _, err = pool.Exec(ctx, `INSERT INTO deployment_audit(deployment_id,account_id,kind,actor,data) VALUES($1,$2,'deploy.mcp_admission','test',$3::jsonb) ON CONFLICT DO NOTHING`, candidate.ID, account, raw); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListDeploymentAudit(ctx, candidate.ID, 100)
	if err != nil || len(rows) != recordCount {
		t.Fatalf("duplicate admission retained: %d %v", len(rows), err)
	}
	// Recovery uses the same transaction guard and records the previous target.
	_, err = s.UpdateDeploymentTraffic(state.WithMCPAccessPolicyExpectation(ctx, account, app, expected), stable, 100, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ListDeploymentAudit(ctx, stable, 100)
	if err != nil || len(recovered) != 1 || !strings.Contains(string(recovered[0].Data), `"outcome": "accepted"`) {
		t.Fatalf("recovery evidence missing: %+v %v", recovered, err)
	}
	// The ongoing monitor follows the recovered serving target and its receipt.
	monitoring, err := s.MCPPolicyConvergenceSnapshot(ctx, account, app)
	if err != nil || monitoring.State != "active" || monitoring.DeploymentID != stable || monitoring.AdmissionID == "" {
		t.Fatalf("monitoring snapshot: %+v %v", monitoring, err)
	}
	if err = s.UpsertGatewayEdgeRuleWatermark(ctx, "mcp-gateway", "00000000-0000-0000-0000-000000000009", 0); err != nil {
		t.Fatal(err)
	}
	monitoring, err = s.MCPPolicyConvergenceSnapshot(ctx, account, app)
	if err != nil || monitoring.State != "pending" || monitoring.UnconvergedGateways != 1 || monitoring.StaleGateways != 0 {
		t.Fatalf("restart not detected: %+v %v", monitoring, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE gateway_edge_rule_watermarks SET observed_at=clock_timestamp()-interval '11 seconds' WHERE node_name='mcp-gateway'`); err != nil {
		t.Fatal(err)
	}
	monitoring, err = s.MCPPolicyConvergenceSnapshot(ctx, account, app)
	if err != nil || monitoring.UnconvergedGateways != 1 || monitoring.StaleGateways != 1 {
		t.Fatalf("stale replay double-counted: %+v %v", monitoring, err)
	}
	if err = s.UpsertGatewayEdgeRuleWatermark(ctx, "mcp-gateway", "00000000-0000-0000-0000-000000000009", revision); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAccount(ctx, "monitor-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MCPPolicyConvergenceSnapshot(ctx, other.ID, app)
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account monitor disclosed app: %v", err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "monitor-other", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,kind,action) VALUES($1,$2,'other.example','/**','jwt','{"kind":"jwt","jwt":{"issuer":"https://other.example"}}')`, other.ID, otherApp.ID)
	if err != nil {
		t.Fatal(err)
	}
	monitoring, err = s.MCPPolicyConvergenceSnapshot(ctx, account, app)
	if err != nil || monitoring.State != "active" || monitoring.DesiredRevision != revision {
		t.Fatalf("other account advanced monitoring: %+v %v", monitoring, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE compute_nodes SET lifecycle='maintenance'`); err != nil {
		t.Fatal(err)
	}
	monitoring, err = s.MCPPolicyConvergenceSnapshot(ctx, account, app)
	if err != nil || monitoring.State != "unverified" || monitoring.ServingGateways != 0 {
		t.Fatalf("missing fleet healthy: %+v %v", monitoring, err)
	}

	paused, err := s.MCPPolicyConvergenceSnapshot(ctx, other.ID, otherApp.ID)
	if err != nil || paused.State != "paused" {
		t.Fatalf("undeployed app was monitored: %+v %v", paused, err)
	}

}
