package connectioncapacity

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestConnectionCapacityCeilings(t *testing.T) {
	for _, test := range []struct {
		name        string
		observation Observation
		allocation  int64
		want        int64
	}{
		{"reservations", Observation{100, 3, 2, -1, -1}, 100, 95},
		{"provider", Observation{100, 3, 2, -1, -1}, 40, 40},
		{"database", Observation{100, 3, 2, 10, -1}, 40, 10},
		{"role", Observation{100, 3, 2, 10, 5}, 40, 5},
		{"zero", Observation{100, 3, 2, 0, -1}, 40, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !test.observation.Valid() || test.observation.Ceiling(test.allocation) != test.want {
				t.Fatal(test.observation)
			}
		})
	}
	for _, invalid := range []Observation{{100, 101, 0, -1, -1}, {100, 99, 2, -1, -1}, {100, -1, 0, -1, -1}, {100, 3, 0, -2, -1}, {}} {
		if invalid.Valid() {
			t.Fatal("accepted invalid SQL limits", invalid)
		}
	}
}

func TestConnectionCapacityPGReadChecksSessionAndCancellation(t *testing.T) {
	pool := pgtest.Open(t)
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var database, role string
	var major int
	var ordinary int64
	if err := conn.QueryRow(t.Context(), `SELECT current_database(),session_user,current_setting('server_version_num')::int/10000,
 current_setting('max_connections')::bigint-current_setting('superuser_reserved_connections')::bigint
 -coalesce(nullif(current_setting('reserved_connections',true),''),'0')::bigint`).Scan(&database, &role, &major, &ordinary); err != nil {
		t.Fatal(err)
	}
	got, err := Read(t.Context(), conn.Conn(), database, role, major)
	if err != nil || !got.Valid() || got.MaxConnections-got.SuperuserReserved-got.Reserved != ordinary {
		t.Fatal(got, err)
	}
	for _, expected := range []struct {
		database, role string
		major          int
	}{{database + "_wrong", role, major}, {database, role + "_wrong", major}, {database, role, major + 1}} {
		if _, err := Read(t.Context(), conn.Conn(), expected.database, expected.role, expected.major); !errors.Is(err, ErrObservation) {
			t.Fatal("session mismatch accepted", err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Read(canceled, conn.Conn(), database, role, major); !errors.Is(err, ErrObservation) {
		t.Fatal("cancellation accepted", err)
	}
}
