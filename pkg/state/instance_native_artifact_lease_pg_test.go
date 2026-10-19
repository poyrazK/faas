//go:build !no_pg

package state

// adr: 435

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgNativeArtifactGrantLease(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	nativeArtifactGrantLease(t, s)
}
func TestPgNativeArtifactMissingScan(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	nativeArtifactMissingScan(t, s)
}
func TestPgNativeArtifactEnforceFindings(t *testing.T) {
	nativeArtifactEnforceFindings(t, func(t *testing.T) nativeArtifactTestStore { s, _ := runtimeCapturePGStore(t); return s })
}
func TestPgNativeArtifactAdvisoryFindings(t *testing.T) {
	nativeArtifactAdvisoryFindings(t, func(t *testing.T) nativeArtifactTestStore { s, _ := runtimeCapturePGStore(t); return s })
}
func TestPgNativeArtifactMissingSignedProducer(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	nativeArtifactMissingSignedProducer(t, s)
}
func TestPgNativeArtifactComposedDatabaseLease(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	nativeArtifactComposedDatabaseLease(t, s)
}
func TestPgNativeArtifactPromotionRenewsApproval(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	nativeArtifactPromotionRenewsApproval(t, s)
}

func TestPgNativeArtifactRawGrantCannotExceedScanLease(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	in, _, app, dep := nativeArtifactFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	scan := publishNativeComposedScan(t, s, app, dep, in.Report)
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	candidate.ExpiresAtUnixNano = scan.ExpiresAt.UnixNano() + 1
	raw, _ := json.Marshal(candidate)
	_, err := pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, candidate.Token, ins.ID, ins.State, raw)
	if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw grant writer escaped the exclusive artifact ceiling: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate); err != nil {
		t.Fatalf("refused raw insert left partial authority: %v", err)
	}
}

func TestPgNativeArtifactRawPublicationRechecksComposedApproval(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	in, base, app, dep := artifactScanBaseFixture(t, s)
	app = manageNativeArtifactApp(t, s, app)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	b := runtimeArtifactBaseScanInput(in, base)
	if _, err := s.PublishBaseImageScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	composed := publishNativeComposedScan(t, s, app, dep, in.Report)
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil {
		t.Fatal(err)
	}
	receipt := nativeArtifactReceipt(grant, false)
	raw, _ := json.Marshal(receipt)
	if _, err := sqlc.New().RecordInstanceApplicationStandardReceipt(t.Context(), pool, sqlc.RecordInstanceApplicationStandardReceiptParams{Token: mustPgUUID(grant.Token), Receipt: raw}); err != nil {
		t.Fatal(err)
	}
	failed := composed.Input
	failed.ID, failed.Status, failed.Reports, failed.Facts.Views, failed.Failure = uuid.NewString(), "failed", nil, nil, "scanner_unavailable"
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), failed); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `UPDATE instances SET application_standard_boot_token=$2,state='running',netns=$3,host_ip=$4,guest_uid=$5 WHERE id=$1`, ins.ID, grant.Token, receipt.Netns, receipt.HostIP, receipt.LeaseUID)
	if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("saved receipt bypassed revoked composed approval: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func TestPgNativeArtifactShortPrivateScanLease(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	component, _, app, dep := nativeArtifactFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	in := nativeComposedScanInput(t, s, app, dep, component.Report)
	in, hash, err := prepareDeploymentRuntimeScan(in)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(in)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	q := sqlc.New()
	if err := q.AuthorizeDeploymentRuntimeScanInsert(t.Context(), tx, mustPgUUID(in.ID)); err != nil {
		t.Fatal(err)
	}
	row, err := q.InsertDeploymentRuntimeScan(t.Context(), tx, sqlc.InsertDeploymentRuntimeScanParams{ID: mustPgUUID(in.ID), DeploymentID: mustPgUUID(in.DeploymentID), InputSnapshot: raw, InputHash: hash, PublisherExpiresAt: standardPgTime(time.Now().Add(time.Hour)), TtlSeconds: 1, DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds()})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SelectDeploymentRuntimeScan(t.Context(), tx, sqlc.SelectDeploymentRuntimeScanParams{ID: row.ID, DeploymentID: row.DeploymentID}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || grant.ExpiresAtUnixNano != row.ExpiresAt.Time.UnixNano() {
		t.Fatalf("short storage-owned scan did not bound grant: %v", err)
	}
	receipt := nativeArtifactReceipt(grant, false)
	time.Sleep(time.Until(row.ExpiresAt.Time) + 20*time.Millisecond)
	raw, _ = json.Marshal(receipt)
	_, err = pool.Exec(t.Context(), `UPDATE instance_application_standard_boots SET receipt=$2::jsonb,received_at=clock_timestamp() WHERE token=$1`, grant.Token, raw)
	if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw receipt bypassed a naturally expired scan: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func TestPgNativeArtifactSidecarLease(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	nativeArtifactSidecarLease(t, s)
}

func TestPgNativeArtifactRawPromotionCannotExceedLease(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	parent, _, deadline := nativeArtifactPausedComposedFixture(t, s)
	p := promotionTestGrant(t, parent)
	p.Binding.ExpiresAtUnixNano = deadline.UnixNano() + 1
	raw, _ := json.Marshal(p.Binding)
	_, err := pool.Exec(t.Context(), `INSERT INTO instance_application_standard_promotions(token,instance_id,parent_token,binding) VALUES($1,$2,$3,$4::jsonb)`, p.Binding.Token, p.Binding.InstanceID, parent.Binding.Token, raw)
	if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw promotion escaped its fresh artifact lease: %v", err)
	}
	if _, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), p); err != nil {
		t.Fatalf("refused raw promotion left partial authority: %v", err)
	}
}

func TestPgNativeArtifactRawEnforceChecksComposedFindings(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	in, base, app, dep := artifactScanBaseFixture(t, s)
	app = manageNativeArtifactApp(t, s, app)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	b := runtimeArtifactBaseScanInput(in, base)
	b.Report.Vulnerabilities, b.Report.SeverityCounts = []api.Vulnerability{{ID: "CVE-native-composed", Severity: "UNKNOWN"}}, api.SeverityCounts{Unknown: 1}
	if _, err := s.PublishBaseImageScan(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	composed := publishNativeComposedScan(t, s, app, dep, b.Report)
	policy := api.AppSecurityPolicyEnforce
	if _, err := s.UpdateApp(t.Context(), app.ID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy}); err != nil {
		t.Fatal(err)
	}
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	evidence, err := s.GetFreshDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidate.ExpiresAtUnixNano = evidence.ExpiresAt.UnixNano()
	if evidence.Scan.ID != composed.ID {
		t.Fatal("composed finding fixture not selected")
	}
	raw, _ := json.Marshal(candidate)
	_, err = pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, candidate.Token, ins.ID, ins.State, raw)
	if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw native issue ignored composed findings: %v", err)
	}
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("Go native issue ignored composed findings: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func TestPgNativeArtifactClockRecheckedAfterRead(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	in, _, app, dep := nativeArtifactFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	deadline := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Second)
	in.Report.ScannerDBBuiltAt = deadline.Add(-api.ApplicationStandardScannerDBMaxAge).Format(time.RFC3339Nano)
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	publishNativeComposedScan(t, s, app, dep, in.Report)
	ins, candidate := nativeArtifactAttempt(t, s, app, dep)
	candidate.ExpiresAtUnixNano = deadline.UnixNano()
	// This private clone delays a successful coherent read. No producer or
	// evidence history is changed, and no production migration is modified.
	_, err := pool.Exec(t.Context(), `ALTER FUNCTION application_standard_native_artifact_deadline(jsonb,timestamptz) RENAME TO native_artifact_deadline_before_test_delay;
 CREATE FUNCTION application_standard_native_artifact_deadline(input jsonb,now_utc timestamptz) RETURNS timestamptz LANGUAGE plpgsql AS $$
 DECLARE deadline timestamptz;
 BEGIN deadline:=native_artifact_deadline_before_test_delay(input,now_utc); PERFORM pg_sleep(2.2); RETURN deadline; END; $$;`)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(candidate)
	started := time.Now()
	_, err = pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, candidate.Token, ins.ID, ins.State, raw)
	if time.Since(started) < 2*time.Second || !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expiry during a successful locked read authorized a grant: %v", err)
	}
	var grants int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM instance_application_standard_boots WHERE instance_id=$1`, ins.ID).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("slow refusal left authority: %d %v", grants, err)
	}
	assertNativeBootUnpublished(t, s, ins)
}
