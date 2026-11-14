package datapipeline

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNeonProviderHintCannotGrantAdmission(t *testing.T) {
	for _, tc := range []struct {
		host       string
		neon, pool bool
	}{
		{"ep-example.us-east-2.aws.neon.tech", true, false},
		{"ep-example-pooler.us-east-2.aws.neon.tech", true, true},
		{"ep-example-pooler.us-east-2.aws.NEON.TECH.", true, true},
		{"ep-example-pooler.neon.tech.attacker.example", false, false},
	} {
		neon, pool := neonSource("postgres://user:password@" + tc.host + "/db?sslmode=verify-full")
		if neon != tc.neon || pool != tc.pool {
			t.Fatal(tc, neon, pool)
		}
	}
	e := &Engine{openSource: func(context.Context, string) (*pgxpool.Pool, error) {
		t.Fatal("pooled source was dialed")
		return nil, nil
	}}
	c := e.Check(t.Context(), ConnectionCheck{}, "postgres://user:password@ep-example-pooler.us-east-2.aws.neon.tech/db?sslmode=verify-full")
	if c.State != "blocked" || c.SourceProviderHint != "neon" || c.Checks[0].Code != "source_neon_direct_connection_required" || c.Capacity != nil {
		t.Fatal("Neon hint bypassed admission", c)
	}
}
