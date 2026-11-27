package receivers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prod hunt 2026-11-21: cron deliveries' "gregale-cron" rows failed the
// ua_family CHECK, dropped whole telemetry batches and held every canary.
func TestRequestTelemetryUAFamilyNeverRefusesARow(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "__unknown__"},
		{"chrome", "chrome"},
		{"gregale-cron", "gregale-cron"},
		{"gregale-queue", "gregale-queue"},
		{"gregale-replay", "gregale-replay"},
		{"gregale-future_source", "other"},
		{"Mozilla/5.0", "other"},
	} {
		if got := requestTelemetryUAFamily(tc.in); got != tc.want {
			t.Errorf("requestTelemetryUAFamily(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The receiver's set must stay inside the column's CHECK: a family the Go
// set passes but the database refuses fails the batch again.
func TestRequestTelemetryUAFamiliesMatchTheMigration(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "20261121130352100_request_telemetry_invocation_ua_family.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	if i := strings.Index(up, "+goose Down"); i >= 0 {
		up = up[:i]
	}
	for family := range requestTelemetryUAFamilies {
		if !strings.Contains(up, "'"+family+"'") {
			t.Errorf("family %q is not in the migration's CHECK", family)
		}
	}
}
