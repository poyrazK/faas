package migrations

import (
	"testing"
	"time"
)

func TestTimestampMigrationVersion(t *testing.T) {
	now := time.Date(2026, time.September, 4, 18, 45, 12, 345_999_999, time.FixedZone("test", 3*60*60))
	if got, want := TimestampMigrationVersion(now), int64(20260904154512345); got != want {
		t.Fatalf("TimestampMigrationVersion() = %d, want %d", got, want)
	}
}

func TestIsTimestampMigrationVersion(t *testing.T) {
	if IsTimestampMigrationVersion(LegacyMigrationMaxVersion) {
		t.Fatal("legacy migration classified as timestamp migration")
	}
	if !IsTimestampMigrationVersion(TimestampMigrationMinVersion) {
		t.Fatal("cutover migration not classified as timestamp migration")
	}
}
