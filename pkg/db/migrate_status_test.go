package db

import (
	"slices"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestMigrationStatusFindsMissingVersionBelowDBMaximum(t *testing.T) {
	const (
		earlier = int64(20260904120000123)
		later   = int64(20260904150000999)
	)
	collected := goose.Migrations{
		{Version: 1, Source: "00001_init.sql"},
		{Version: earlier, Source: "20260904120000123_add_region.sql"},
		{Version: later, Source: "20260904150000999_add_job_priority.sql"},
	}
	applied := map[int64]struct{}{0: {}, 1: {}, later: {}}

	status := migrationStatusFrom(later, collected, applied)
	if status.DBVersion != later || status.MaxEmbedded != later {
		t.Fatalf("versions: DB=%d maxEmbedded=%d, want %d", status.DBVersion, status.MaxEmbedded, later)
	}
	if !slices.Equal(status.EmbeddedVersions, []int64{1, earlier, later}) {
		t.Fatalf("embedded = %v", status.EmbeddedVersions)
	}
	if !slices.Equal(status.Pending, []string{"20260904120000123_add_region.sql"}) {
		t.Fatalf("pending = %v, want earlier timestamp migration", status.Pending)
	}
}
