package trafficguard_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func TestPgMCPAdmissionRollsBackTrafficAndRetainsRejection(t *testing.T) {
	s, pool, ctx := openMCPAccessStore(t)
	account, app, stable := seedMCPAccessLive(t, s, ctx, "admission-atomic")
	candidate, err := s.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: strings.Repeat("d", 64), Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	expected := pgMCPAccessExpectation(t, s, ctx, account)
	_, err = pool.Exec(ctx, `CREATE FUNCTION fail_admission_traffic() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PRIVATE_DATABASE_ERROR'; END $$; CREATE TRIGGER fail_admission_traffic BEFORE UPDATE OF traffic_percent ON deployments FOR EACH ROW EXECUTE FUNCTION fail_admission_traffic()`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateDeploymentTraffic(state.WithMCPAccessPolicyExpectation(ctx, account, app, expected), candidate.ID, 100, stable)
	if err == nil {
		t.Fatal("injected traffic failure accepted")
	}
	target, _ := s.DeploymentByID(ctx, candidate.ID)
	prior, _ := s.DeploymentByID(ctx, stable)
	if target.TrafficPercent != 0 || prior.TrafficPercent != 100 {
		t.Fatal("failed transaction moved traffic")
	}
	rows, err := s.ListDeploymentAudit(ctx, candidate.ID, 100)
	if err != nil || len(rows) != 1 || !strings.Contains(string(rows[0].Data), `"reason": "traffic_update_failed"`) || strings.Contains(string(rows[0].Data), "PRIVATE_DATABASE_ERROR") {
		t.Fatalf("rejection missing or leaked error: %+v %v", rows, err)
	}
	_, err = pool.Exec(ctx, `DROP TRIGGER fail_admission_traffic ON deployments; DROP FUNCTION fail_admission_traffic(); CREATE FUNCTION fail_admission_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='deploy.mcp_admission' THEN RAISE EXCEPTION 'PRIVATE_AUDIT_ERROR'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_admission_audit BEFORE INSERT ON deployment_audit FOR EACH ROW EXECUTE FUNCTION fail_admission_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateDeploymentTraffic(state.WithMCPAccessPolicyExpectation(ctx, account, app, expected), candidate.ID, 100, stable)
	if !errors.Is(err, state.ErrMCPAdmissionPersistence) {
		t.Fatalf("audit storage failure not surfaced: %v", err)
	}
	target, _ = s.DeploymentByID(ctx, candidate.ID)
	prior, _ = s.DeploymentByID(ctx, stable)
	if target.TrafficPercent != 0 || prior.TrafficPercent != 100 {
		t.Fatal("unrecorded acceptance moved traffic")
	}
	rows, err = s.ListDeploymentAudit(ctx, candidate.ID, 100)
	if err != nil || len(rows) != 1 {
		t.Fatalf("failed acceptance was retained: %+v %v", rows, err)
	}
}
