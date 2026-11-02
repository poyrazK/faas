// adr: 1027 — database hosts and managed_postgres validate together.
package manifest

import (
	"strings"
	"testing"
)

const databaseHost = `    - name: db-1
      role: database
      address: 10.42.0.5:50051
      database:
        admission_ceiling_mb: 48000
        volume_capacity_bytes: 3000000000000
        postgres_majors: [16, 17]
`

func withDatabase(host, managed string) string {
	body := strings.Replace(validManifest, "      address: 10.42.0.2:50051\n", "      address: 10.42.0.2:50051\n"+host, 1)
	return body + managed
}

func TestValidate_ManagedPostgres(t *testing.T) {
	const managed = "managed_postgres:\n  host_suffix: db.gregale.dev\n"
	m, err := Parse([]byte(withDatabase(databaseHost, managed)))
	if err != nil {
		t.Fatal(err)
	}
	if errs := m.Validate(); errs != nil {
		t.Fatalf("Validate = %v", errs)
	}
	if hosts := m.Fleet.DatabaseHosts(); len(hosts) != 1 || hosts[0].Database.AdmissionCeilingMB != 48000 || !hosts[0].Database.Serving() {
		t.Fatalf("database hosts = %+v", hosts)
	}
	for _, lifecycle := range []string{"active", "draining", "unavailable"} {
		m, err := Parse([]byte(withDatabase(databaseHost+"        lifecycle: "+lifecycle+"\n", "managed_postgres:\n  host_suffix: db.gregale.dev\n  shared_archive: true\n  evacuate_draining: true\n")))
		if err != nil {
			t.Fatal(err)
		}
		if errs := m.Validate(); errs != nil {
			t.Fatalf("lifecycle %s: Validate = %v", lifecycle, errs)
		}
		if serving := m.Fleet.DatabaseHosts()[0].Database.Serving(); serving != (lifecycle != "unavailable") {
			t.Fatalf("lifecycle %s: Serving = %v", lifecycle, serving)
		}
	}
	for name, tc := range map[string]struct{ body, want string }{
		"no managed_postgres":                 {withDatabase(databaseHost, ""), "managed_postgres: is required"},
		"no database host":                    {withDatabase("", managed), "managed_postgres: needs at least one database host"},
		"bad suffix":                          {withDatabase(databaseHost, "managed_postgres:\n  host_suffix: \"not a host\"\n"), "managed_postgres.host_suffix"},
		"no database block":                   {withDatabase("    - name: db-1\n      role: database\n      address: 10.42.0.5:50051\n", managed), "fleet.hosts[2].database: is required"},
		"block on compute":                    {strings.Replace(withDatabase(databaseHost, managed), "      address: 10.42.0.2:50051\n", "      address: 10.42.0.2:50051\n      database:\n        admission_ceiling_mb: 1\n        volume_capacity_bytes: 1\n", 1), "fleet.hosts[1].database: is only valid on a database host"},
		"zero ceiling":                        {withDatabase(strings.Replace(databaseHost, "admission_ceiling_mb: 48000", "admission_ceiling_mb: 0", 1), managed), "admission_ceiling_mb: must be positive"},
		"bad major":                           {withDatabase(strings.Replace(databaseHost, "[16, 17]", "[13]", 1), managed), "major 13"},
		"address outside mesh":                {withDatabase(strings.Replace(databaseHost, "10.42.0.5", "10.43.0.5", 1), managed), "outside overlay.cidr"},
		"bad lifecycle":                       {withDatabase(databaseHost+"        lifecycle: retired\n", managed), `lifecycle "retired"`},
		"evacuation without a shared archive": {withDatabase(databaseHost, managed+"  evacuate_draining: true\n"), "managed_postgres.evacuate_draining: needs shared_archive"},
		"drills too often":                    {withDatabase(databaseHost, managed+"  drill_every: 10m\n"), "managed_postgres.drill_every"},
		"drills not a duration":               {withDatabase(databaseHost, managed+"  drill_every: daily\n"), "managed_postgres.drill_every"},
	} {
		t.Run(name, func(t *testing.T) {
			m, err := Parse([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			errs := m.Validate()
			if errs == nil || !strings.Contains(errs.Error(), tc.want) {
				t.Fatalf("Validate = %v, want %q", errs, tc.want)
			}
		})
	}
}
