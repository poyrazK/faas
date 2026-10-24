// adr: 20261023181931 — a rollback switch activates its cutover in one transaction under the rollback bar.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func activationFixture(t *testing.T) (*PostgresStore, *state.PgStore, Cutover, state.Deployment, state.ComputeNode) {
	t.Helper()
	s, ps, c, node := admissionCutoverFixture(t)
	if _, err := s.pool.Exec(t.Context(), `UPDATE apps SET node_id=$2 WHERE id=$1`, c.AppID, node.ID); err != nil {
		t.Fatal(err)
	}
	live, err := ps.CreateDeployment(t.Context(), state.Deployment{AppID: c.AppID, Scope: "default", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE deployments SET status='live',traffic_percent=100 WHERE id=$1`, live.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.CreateInstanceWithMode(t.Context(), c.AppID, live.ID, "running", 256, node.ID, "", "normal"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.CreateInstanceWithMode(t.Context(), c.AppID, live.ID, "parked", 256, node.ID, "", "normal"); err != nil {
		t.Fatal(err)
	}
	return s, ps, c, live, node
}

func activationBlockedWith(t *testing.T, err error, code string) {
	t.Helper()
	if got := ActivationBlockedCode(err); got != code {
		t.Fatalf("activation err = %v, want blocked %s", err, code)
	}
}

func TestPostgresCutoverActivationMovesTheBindingsUnderTheRollbackBar(t *testing.T) {
	s, ps, c, live, node := activationFixture(t)
	ctx := context.Background()
	request := func(fencedAt time.Time, liveID string, now time.Time) CutoverActivation {
		return CutoverActivation{AccountID: c.AccountID, CutoverID: c.ID, FencedAt: fencedAt, LiveDeploymentID: liveID, Now: now}
	}
	now := time.Now().UTC()
	_, err := s.ActivateCutover(ctx, request(now, live.ID, now))
	activationBlockedWith(t, err, ActivationFenceChanged)

	fence, err := s.FenceCutoverAdmission(ctx, c.AccountID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ActivateCutover(ctx, request(fence.FencedAt, live.ID, now))
	activationBlockedWith(t, err, ActivationDrainIncomplete)
	plan, err := ps.ReserveManagedPostgresDrainPlan(ctx, c.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ActivateCutover(ctx, request(fence.FencedAt, live.ID, now))
	activationBlockedWith(t, err, ActivationDrainIncomplete)
	parked := 0
	for _, m := range plan.Members {
		if m.BlockerCode == "parked_instance_unproven" {
			parked++
			continue
		}
		if err := ps.CompleteManagedPostgresDrain(ctx, m.Drain.Frame, cutoverDrainProof(m.Drain.Frame)); err != nil {
			t.Fatal(err)
		}
	}
	if parked != 1 {
		t.Fatalf("plan did not keep the parked member: %+v", plan.Members)
	}
	_, err = s.ActivateCutover(ctx, request(fence.FencedAt, uuid.NewString(), now))
	activationBlockedWith(t, err, ActivationReleaseChanged)
	_, err = s.ActivateCutover(ctx, request(fence.FencedAt, live.ID, now.Add(6*time.Minute)))
	activationBlockedWith(t, err, ActivationVerificationStale)
	_, err = s.ActivateCutover(ctx, request(fence.FencedAt.Add(time.Second), live.ID, now))
	activationBlockedWith(t, err, ActivationFenceChanged)

	sourceSecrets := map[string][]byte{}
	for _, m := range c.Credentials {
		var ciphertext []byte
		if err := s.pool.QueryRow(ctx, `SELECT ciphertext FROM app_secrets WHERE managed_postgres_binding_id=$1`, m.SourceBindingID).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		sourceSecrets[m.EnvironmentKey] = ciphertext
	}
	out, err := s.ActivateCutover(ctx, request(fence.FencedAt, live.ID, now))
	if err != nil {
		t.Fatal(err)
	}
	if !out.SecretsPublished || len(out.Published) != len(c.Credentials) || len(out.RetiredSources) != len(c.Credentials) {
		t.Fatalf("activation result %+v", out)
	}
	activated, err := s.GetCutover(ctx, c.AccountID, c.ID)
	if err != nil || activated.State != CutoverActivated {
		t.Fatalf("cutover %+v %v", activated, err)
	}
	for _, m := range activated.Credentials {
		if m.State != "published" || len(m.Sealed.Ciphertext) != 0 || !m.VerifiedAt.IsZero() {
			t.Fatalf("staged envelope kept after publication: %+v", m)
		}
	}
	for _, m := range c.Credentials {
		var state, databaseID string
		var cutover *string
		if err := s.pool.QueryRow(ctx, `SELECT state,database_id::text,cutover_id::text FROM managed_postgres_bindings WHERE id=$1`, m.SourceBindingID).Scan(&state, &databaseID, &cutover); err != nil {
			t.Fatal(err)
		}
		if state != "deleted" || databaseID != c.Source.ID || cutover != nil {
			t.Fatalf("source binding %s is %s on %s pinned %v", m.SourceBindingID, state, databaseID, cutover)
		}
		var bindingID string
		var ciphertext []byte
		if err := s.pool.QueryRow(ctx, `SELECT managed_postgres_binding_id::text,ciphertext FROM app_secrets WHERE app_id=$1 AND scope='default' AND key=$2`,
			c.AppID, m.EnvironmentKey).Scan(&bindingID, &ciphertext); err != nil {
			t.Fatal(err)
		}
		if bindingID != m.ID || string(ciphertext) != string(m.Sealed.Ciphertext) || string(ciphertext) == string(sourceSecrets[m.EnvironmentKey]) {
			t.Fatalf("%s points at %s, want the staged credential %s", m.EnvironmentKey, bindingID, m.ID)
		}
		if err := s.pool.QueryRow(ctx, `SELECT state,database_id::text FROM managed_postgres_bindings WHERE id=$1`, m.ID).Scan(&state, &databaseID); err != nil {
			t.Fatal(err)
		}
		if state != "ready" || databaseID != c.Target.ID {
			t.Fatalf("target binding %s is %s on %s", m.ID, state, databaseID)
		}
	}
	var pinned int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM managed_postgres_databases WHERE cutover_id=$1`, c.ID).Scan(&pinned); err != nil || pinned != 0 {
		t.Fatalf("databases still pinned: %d %v", pinned, err)
	}
	var stamped int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM app_runtime_config_changes WHERE app_id=$1`, c.AppID).Scan(&stamped); err != nil || stamped != 1 {
		t.Fatalf("runtime config not stamped: %d %v", stamped, err)
	}

	_, err = s.ActivateCutover(ctx, request(fence.FencedAt, live.ID, now))
	activationBlockedWith(t, err, ActivationNotVerified)
	if _, err := s.CancelCutover(ctx, c.AccountID, c.ID, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancel after activation err = %v, want ErrConflict", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE managed_postgres_cutovers SET state='cancelling' WHERE id=$1`, c.ID); err == nil {
		t.Fatal("an activated cutover changed state")
	}
	var fenced bool
	if err := s.pool.QueryRow(ctx, `SELECT managed_postgres_admission_cutover_id IS NOT NULL FROM apps WHERE id=$1`, c.AppID).Scan(&fenced); err != nil || !fenced {
		t.Fatalf("activation lifted the fence before resume: %v %v", fenced, err)
	}
	for range 2 {
		if err := s.ReleaseCutoverAdmission(ctx, c.AccountID, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.pool.QueryRow(ctx, `SELECT managed_postgres_admission_cutover_id IS NOT NULL FROM apps WHERE id=$1`, c.AppID).Scan(&fenced); err != nil || fenced {
		t.Fatalf("resume kept the fence: %v %v", fenced, err)
	}
}

func TestPostgresCutoverActivationRefusesADrainMemberItCannotRetire(t *testing.T) {
	s, ps, c, live, node := activationFixture(t)
	ctx := context.Background()
	d, err := ps.CreateDeployment(ctx, state.Deployment{AppID: c.AppID, Scope: "default", Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.CreateInstanceWithMode(ctx, c.AppID, d.ID, "pending", 256, node.ID, "", "normal"); err != nil {
		t.Fatal(err)
	}
	fence, err := s.FenceCutoverAdmission(ctx, c.AccountID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ps.ReserveManagedPostgresDrainPlan(ctx, c.ID, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range plan.Members {
		if m.Drain != nil {
			if err := ps.CompleteManagedPostgresDrain(ctx, m.Drain.Frame, cutoverDrainProof(m.Drain.Frame)); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err = s.ActivateCutover(ctx, CutoverActivation{AccountID: c.AccountID, CutoverID: c.ID, FencedAt: fence.FencedAt, LiveDeploymentID: live.ID, Now: time.Now().UTC()})
	activationBlockedWith(t, err, ActivationDrainIncomplete)
	if got, err := s.GetCutover(ctx, c.AccountID, c.ID); err != nil || got.State != CutoverVerified {
		t.Fatalf("refused activation changed the cutover: %+v %v", got, err)
	}
	preview, err := ps.PreviewDrainBlockers(ctx, c.AppID)
	if err != nil || preview.Blocking != 1 || preview.Parked != 1 || preview.Settled() {
		t.Fatalf("preview %+v %v", preview, err)
	}
}
