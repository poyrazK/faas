package db

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// annotateSchemaDrift is the difference between an operator reading
//
//	ERROR: column "source_url" of relation "deployments" already exists (SQLSTATE 42701)
//
// and understanding that the box's schema is ahead of its goose ledger. These
// tests pin which SQLSTATEs get the diagnosis and, just as importantly, which
// do not — a genuine SQL bug must keep its own error text.
func TestAnnotateSchemaDrift(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantDrift bool
		wantKind  string
	}{
		{
			// The 00053_deployments_source_url deploy failure, verbatim.
			name:      "duplicate column (00053)",
			err:       &pgconn.PgError{Code: "42701", Message: `column "source_url" of relation "deployments" already exists`},
			wantDrift: true,
			wantKind:  "column",
		},
		{
			// The 00030_invocations deploy failure.
			name:      "duplicate relation (00030)",
			err:       &pgconn.PgError{Code: "42P07", Message: `relation "invocations" already exists`},
			wantDrift: true,
			wantKind:  "relation (table/index/view)",
		},
		{
			name:      "duplicate object (constraint)",
			err:       &pgconn.PgError{Code: "42710", Message: `constraint "x_chk" already exists`},
			wantDrift: true,
			wantKind:  "object (constraint/type/role)",
		},
		{
			// A real bug in a new migration must NOT be dressed up as drift.
			name:      "syntax error is not drift",
			err:       &pgconn.PgError{Code: "42601", Message: "syntax error at or near"},
			wantDrift: false,
		},
		{
			name:      "undefined table is not drift",
			err:       &pgconn.PgError{Code: "42P01", Message: `relation "nope" does not exist`},
			wantDrift: false,
		},
		{
			name:      "non-postgres error passes through",
			err:       errors.New("connection refused"),
			wantDrift: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := annotateSchemaDrift(tc.err)

			var drift *SchemaDriftError
			isDrift := errors.As(got, &drift)
			if isDrift != tc.wantDrift {
				t.Fatalf("drift = %v, want %v (err = %v)", isDrift, tc.wantDrift, got)
			}
			if !tc.wantDrift {
				// Pass-through means untouched: same identity chain (Is) and
				// same text (nothing wrapped around it).
				if !errors.Is(got, tc.err) || got.Error() != tc.err.Error() {
					t.Errorf("non-drift error was modified: got %q, want the original %q", got, tc.err)
				}
				return
			}
			if drift.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", drift.Kind, tc.wantKind)
			}
			// The message has to carry the actionable part, not just a label.
			for _, want := range []string{"AHEAD of goose_db_version", "append-only", "migrate -status"} {
				if !strings.Contains(drift.Error(), want) {
					t.Errorf("diagnosis missing %q:\n%s", want, drift.Error())
				}
			}
			// The original error must stay reachable for anyone unwrapping.
			if !errors.Is(got, tc.err) {
				t.Errorf("Unwrap lost the underlying error")
			}
		})
	}
}

// The annotation must survive being wrapped by MigrateUp's own fmt.Errorf,
// which is how callers actually see it.
func TestAnnotateSchemaDrift_ThroughWrapping(t *testing.T) {
	pg := &pgconn.PgError{Code: "42P07", Message: `relation "invocations" already exists`}
	wrapped := fmt.Errorf("db: goose up: %w", annotateSchemaDrift(fmt.Errorf("goose: %w", pg)))

	var drift *SchemaDriftError
	if !errors.As(wrapped, &drift) {
		t.Fatalf("SchemaDriftError not reachable through wrapping: %v", wrapped)
	}
	if drift.SQLState != "42P07" {
		t.Errorf("SQLState = %q, want 42P07", drift.SQLState)
	}
}
