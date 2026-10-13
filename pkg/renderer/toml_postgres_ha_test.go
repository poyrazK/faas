// adr: 1237
// toml_postgres_ha_test.go pins the Patroni DSN rule (ADR-1237 §3): with
// manifest.postgresql.ha the primary may run on another control node, so a
// control-plane TOML must not pin the local peer socket; the daemons take
// the HAProxy DSN from sealed.env instead.

package renderer

import (
	"strings"
	"testing"
)

// adr: 1237
func TestRenderTOML_ControlPlaneUnderPostgresHAUsesEnvironmentDSN(t *testing.T) {
	const sharedDSN = "postgres://faas@fsn-1.gregale.dev:5432/faas?sslmode=disable"
	for _, d := range []string{"apid", "schedd", "meterd", "githubd"} {
		body, flat, err := renderTOML(tomlRenderCtx{
			Daemon:     d,
			DC:         fixtureTOML(d),
			DBURL:      sharedDSN,
			HostRole:   "control-plane",
			PostgresHA: true,
		})
		if err != nil {
			t.Fatalf("%s: renderTOML: %v", d, err)
		}
		if got := flat["db_url"]; got != "" {
			t.Errorf("%s flat db_url = %q, want empty so sealed.env's HAProxy DSN applies", d, got)
		}
		if strings.Contains(string(body), "/run/postgresql") {
			t.Errorf("%s TOML still pins the local peer socket under Patroni:\n%s", d, body)
		}
	}
}
