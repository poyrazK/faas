package rehearsalpg

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func startupForTest(values ...string) []byte {
	body := strings.Join(values, "\x00") + "\x00\x00"
	out := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(out, uint32(len(out)))
	binary.BigEndian.PutUint32(out[4:], 196608)
	copy(out[8:], body)
	return out
}

func TestPostgresProxyRechecksAdmissionBeforeAnyBackendConnection(t *testing.T) {
	for _, mode := range []string{"wrong database", "expired caller", "revoked during handshake", "authorized"} {
		t.Run(mode, func(t *testing.T) {
			backend, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = backend.Close() }()
			var contacted atomic.Int32
			go func() {
				c, e := backend.Accept()
				if e == nil {
					contacted.Add(1)
					_ = c.Close()
				}
			}()
			r := Resource{ID: "fixture", Status: "ready", RoleName: "fixture_role", DatabaseName: "fixture_db", ExpiresAt: time.Now().Add(time.Minute)}
			calls := 0
			proxy := Proxy{Target: backend.Addr().String(), Resolve: func(context.Context, string) (Resource, error) {
				calls++
				out := r
				if mode == "expired caller" || (mode == "revoked during handshake" && calls == 2) {
					out.Status = "sealing"
				}
				return out, nil
			}}
			front, client := net.Pipe()
			done := make(chan struct{})
			go func() { proxy.ServeConn(t.Context(), front); close(done) }()
			_ = client.SetDeadline(time.Now().Add(time.Second))
			database := r.DatabaseName
			if mode == "wrong database" {
				database = "other_fixture"
			}
			_, _ = client.Write(startupForTest("user", r.RoleName, "database", database))
			_, _ = io.ReadAll(client)
			_ = client.Close()
			<-done
			if got := contacted.Load(); (got > 0) != (mode == "authorized") {
				t.Fatalf("mode %s reached backend %d times", mode, got)
			}
		})
	}
}
func TestPostgresStartupRequiresExactJournalDatabaseAndRole(t *testing.T) {
	r := Resource{RoleName: "test-role", DatabaseName: "test-database"}
	for _, tc := range []struct {
		name   string
		fields []string
		ok     bool
	}{
		{"standard driver", []string{"user", r.RoleName, "database", r.DatabaseName, "application_name", "import", "client_encoding", "UTF8"}, true},
		{"other database", []string{"user", r.RoleName, "database", "postgres"}, false},
		{"other role", []string{"user", "postgres", "database", r.DatabaseName}, false},
		{"duplicate user", []string{"user", r.RoleName, "database", r.DatabaseName, "user", "postgres"}, false},
		{"options", []string{"user", r.RoleName, "database", r.DatabaseName, "options", "-c role=postgres"}, false},
		{"replication", []string{"user", r.RoleName, "database", r.DatabaseName, "replication", "database"}, false},
		{"missing database", []string{"user", r.RoleName}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchingStartup(startupForTest(tc.fields...), r); got != tc.ok {
				t.Fatalf("startup acceptance %v", got)
			}
		})
	}
}
