package db

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestApplyRuntimeRole(t *testing.T) {
	cases := []struct {
		name     string
		role     string
		dsn      string
		wantUser string
		wantErr  string
	}{
		{name: "unset keeps the DSN user", dsn: "postgres:///faas?host=/run/postgresql&user=faas", wantUser: "faas"},
		{name: "peer DSN switches to the role", role: "faas_schedd", dsn: "postgres:///faas?host=/run/postgresql&user=faas", wantUser: "faas_schedd"},
		{name: "surrounding space is ignored", role: " faas_apid ", dsn: "postgres:///faas?host=/run/postgresql&user=faas", wantUser: "faas_apid"},
		{name: "password DSN for the same role", role: "faas_compute", dsn: "postgres://faas_compute:pw@10.0.0.1:5432/faas", wantUser: "faas_compute"},
		{name: "password DSN for another user", role: "faas_compute", dsn: "postgres://faas:pw@10.0.0.1:5432/faas", wantErr: "cannot reuse the password"},
		{name: "owner is not a runtime role", role: "faas", dsn: "postgres:///faas?host=/run/postgresql&user=faas", wantErr: "is not a runtime role"},
		{name: "foreign role", role: "postgres", dsn: "postgres:///faas?host=/run/postgresql&user=faas", wantErr: "is not a runtime role"},
		{name: "quoted identifier", role: `faas_"x`, dsn: "postgres:///faas?host=/run/postgresql&user=faas", wantErr: "is not a runtime role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(RuntimeRoleEnv, tc.role)
			cfg, err := pgx.ParseConfig(tc.dsn)
			if err != nil {
				t.Fatal(err)
			}
			err = applyRuntimeRole(cfg)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.User != tc.wantUser {
				t.Fatalf("user = %q, want %q", cfg.User, tc.wantUser)
			}
		})
	}
}
