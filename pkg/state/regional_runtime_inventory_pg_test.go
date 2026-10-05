//go:build !no_pg

package state

// adr: 635
import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGRegionalRuntimeInventoryContract(t *testing.T) {
	s, _, cell, account := releasePGFixture(t)
	runRegionalInventoryContract(t, s, account, cell)
}

func TestPGRegionalRuntimeResourceSQLGuards(t *testing.T) {
	s, pool, cell, account := releasePGFixture(t)
	f := newRegionalCapacityFixture(t, s, s, account, cell)
	p := f.permit(runtimequota.CapacityPrime)
	f.reserve(t, p)
	claims, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{"RAMMB":128,"VCPU":1,"CPUMillicores":0}`,
		`{"RAMMB":true,"VCPU":1,"CPUMillicores":1000}`,
		`{"RAMMB":128,"VCPU":null,"CPUMillicores":1000}`,
		`{"RAMMB":128,"VCPU":1,"CPUMillicores":0.5}`,
		`{"RAMMB":128.0,"VCPU":1,"CPUMillicores":1000}`,
		`{"RAMMB":2147483648,"VCPU":1,"CPUMillicores":1000}`,
		`{"RAMMB":128,"VCPU":1,"CPUMillicores":1000,"ignored":42}`,
	} {
		_, err := (&sqlc.Queries{}).InsertRegionalRuntimeLaunchSource(t.Context(), pool, sqlc.InsertRegionalRuntimeLaunchSourceParams{
			AppID: uuidToPgtype(f.app()), PermitID: uuidToPgtype(p.ID), InstanceKey: p.InstanceID, PermitClaims: claims, ResourceClaims: []byte(invalid),
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "regional_launch_resources_shape" {
			t.Fatal("invalid native resource claims passed SQL shape guard", invalid, err)
		}
		if _, err := s.RegionalRuntimeLaunchSource(t.Context(), f.app(), p.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("invalid source persisted", err)
		}
	}
}
