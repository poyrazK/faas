package main

import (
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
)

func TestMigrationAdmissionRequiresStagedRoleAndDatabase(t *testing.T) {
	gate := &migrationAdmission{database: "gregale", roles: []string{"staged"}}
	for _, tc := range []struct {
		name       string
		parameters map[string]string
		allow      bool
	}{
		{"staged", map[string]string{"database": "gregale", "user": "staged"}, true},
		{"existing", map[string]string{"database": "gregale", "user": "existing"}, false},
		{"wrong database", map[string]string{"database": "postgres", "user": "staged"}, false},
		{"replication", map[string]string{"database": "gregale", "user": "staged", "replication": "database"}, false},
		{"implicit database", map[string]string{"user": "staged"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			startup := pgproto3.StartupMessage{ProtocolVersion: pgproto3.ProtocolVersionNumber, Parameters: tc.parameters}
			packet, err := startup.Encode(nil)
			if err != nil {
				t.Fatal(err)
			}
			client, server := net.Pipe()
			defer server.Close()
			go func() {
				defer client.Close()
				_, _ = client.Write(packet)
			}()
			admitted := gate.startup(server)
			if (len(admitted) > 0) != tc.allow {
				t.Fatalf("admitted=%v, want %v", len(admitted) > 0, tc.allow)
			}
			if tc.allow {
				var forwarded pgproto3.StartupMessage
				if err := forwarded.Decode(admitted[4:]); err != nil || forwarded.Parameters["user"] != "staged" || forwarded.Parameters["database"] != "gregale" {
					t.Fatalf("unexpected forwarded packet: %+v, %v", forwarded, err)
				}
			}
		})
	}
}
