//go:build !no_pg

package state

// adr: 633
import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGRegionalRuntimeLaunchSourceContract(t *testing.T) {
	runRegionalLaunchSourceContract(t, pgRegionalCapacityFixture)
}

func TestPGRegionalRuntimeRetirementCompositeCursor(t *testing.T) {
	runRegionalRetirementCompositeCursor(t, pgRegionalCapacityFixture)
}

func TestPGRegionalRuntimeLaunchSourceGuardsAndRestart(t *testing.T) {
	f := pgRegionalCapacityFixture(t)
	s := f.ledger.(*PgStore)
	p := initialRegionalPlacement(f, runtimequota.CapacityWarm)
	source := runtimequota.LaunchSource{Permit: p.Permit, InstanceKey: p.Permit.InstanceID}
	if err := s.IssueRegionalRuntimeLaunch(t.Context(), p, source); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE regional_runtime_launch_sources SET instance_key=replace(instance_key,'-','')`,
		`UPDATE regional_runtime_launch_sources SET permit_claims=jsonb_set(permit_claims,'{RunID}',to_jsonb(gen_random_uuid()::text))`,
		`UPDATE regional_runtime_launch_sources SET created_at=created_at+interval '1 second'`,
		`DELETE FROM regional_runtime_launch_sources`,
		`TRUNCATE regional_runtime_launch_sources`,
		`TRUNCATE regional_runtime_capacity_receipts CASCADE`,
	} {
		if _, err := s.pool.Exec(t.Context(), statement); err == nil {
			t.Fatalf("SQL changed retained source: %s", statement)
		}
	}
	if _, err := s.BeginRegionalRuntimeRetirement(t.Context(), p.Permit, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	before, err := s.RegionalRuntimeLaunchSource(t.Context(), f.app(), p.Permit.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "DELETE FROM apps WHERE id=$1", f.app()); err != nil {
		t.Fatal(err)
	}
	restarted := NewPgStore(s.pool)
	after, err := restarted.RegionalRuntimeLaunchSource(t.Context(), f.app(), p.Permit.ID)
	if err != nil || before != after {
		t.Fatal("collection/restart lost original source", after, err)
	}
	rows, err := restarted.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), RegionalRuntimeRetirementCursor{}, 10)
	if err != nil || len(rows) != 1 || rows[0].Source != source {
		t.Fatal("collected attempt not recoverable", rows, err)
	}
}

func TestPGRegionalRuntimeLaunchSourceRefusesSQLBackfillAndMismatchedSource(t *testing.T) {
	for _, fail := range []string{"issued", "fenced", "wrong-key", "wrong-permit", "repeatable-read"} {
		t.Run(fail, func(t *testing.T) {
			f := pgRegionalCapacityFixture(t)
			s := f.ledger.(*PgStore)
			p := f.permit(runtimequota.CapacityWarm)
			f.reserve(t, p)
			key := p.InstanceID
			claims, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			iso := pgx.ReadCommitted
			switch fail {
			case "issued":
				f.issue(t, p)
			case "fenced":
				if _, err := s.BeginRegionalRuntimeRetirement(t.Context(), p, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			case "wrong-key":
				key = uuid.NewString()
			case "wrong-permit":
				p.RunID = uuid.NewString()
				claims, err = json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
			case "repeatable-read":
				iso = pgx.RepeatableRead
			}
			tx, err := s.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: iso})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			_, err = (&sqlc.Queries{}).InsertRegionalRuntimeLaunchSource(t.Context(), tx, sqlc.InsertRegionalRuntimeLaunchSourceParams{AppID: uuidToPgtype(f.app()), PermitID: uuidToPgtype(p.ID), InstanceKey: key, PermitClaims: claims})
			if err == nil {
				t.Fatal("SQL accepted fabricated original source")
			}
		})
	}
}
