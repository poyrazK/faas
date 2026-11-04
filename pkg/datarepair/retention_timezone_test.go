package datarepair

// ADR-20261103201136: preserve frozen retention selections across connections.

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRepairRetentionFrozenTimezone(t *testing.T) {
	for _, zone := range []string{"UTC", "Europe/Istanbul", "America/New_York", "Asia/Kathmandu"} {
		t.Run(zone, func(t *testing.T) {
			e, scope, source := fixture(t, 1)
			parent, err := e.Plan(t.Context(), scope, source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.Replace(t.Context(), scope, parent.ID, parent.Digest, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			ageRetentionFixture(t, e)
			previewer := timezoneEngine(t, e, zone)
			executor := timezoneEngine(t, e, "Pacific/Auckland")
			preview := retentionPreview(t, previewer, scope, 10)
			if len(preview.Entries) != 2 || preview.Entries[0].Kind != "plan" || preview.Entries[1].Kind != "plan" {
				t.Fatalf("missing plan inventory: %+v", preview)
			}
			var proof bool
			for _, entry := range preview.Entries {
				proof = proof || entry.SupersededAt != nil
			}
			if !proof {
				t.Fatal("replacement timestamp proof was not exercised")
			}
			retentionPurge(t, executor, scope, preview)
			assertRepairTimezone(t, executor, "Pacific/Auckland")
			// The next batch covers operation timestamps as well as plan dates.
			journal := retentionPreview(t, previewer, scope, 10)
			if len(journal.Entries) != 1 || journal.Entries[0].Kind != "operation" {
				t.Fatalf("missing operation inventory: %+v", journal)
			}
			retentionPurge(t, executor, scope, journal)
			retentionPurge(t, previewer, scope, preview)
			assertRepairTimezone(t, executor, "Pacific/Auckland")
			assertRepairTimezone(t, previewer, zone)
		})
	}
}

func TestRepairRetentionLegacyTimezonePreviewPreserved(t *testing.T) {
	e, scope, source := fixture(t, 1)
	if _, err := e.Plan(t.Context(), scope, source); err != nil {
		t.Fatal(err)
	}
	ageRetentionFixture(t, e)
	legacy := embeddedFunction(retentionSchema, "prepare_retention")[0]
	legacy = strings.Replace(legacy, ",\n  'render_timezone',current_setting('TimeZone')", "", 1)
	if _, err := e.Pool.Exec(t.Context(), schemaBounds().Replace(legacy)); err != nil {
		t.Fatal(err)
	}
	previewer := timezoneEngine(t, e, "Europe/Istanbul")
	preview := retentionPreview(t, previewer, scope, 10)
	var before, after string
	query := `SELECT document::text||digest FROM gregale_repair.retention_runs WHERE id=$1`
	if err := e.Pool.QueryRow(t.Context(), query, preview.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(t.Context(), UpgradeSchema()); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(t.Context(), query, preview.ID).Scan(&after); err != nil || before != after {
		t.Fatalf("upgrade rewrote reviewed retention selection: %v", err)
	}
	other := timezoneEngine(t, e, "UTC")
	if _, err := other.PurgeRetention(t.Context(), scope, preview.ID, preview.Digest, "legacy-other-zone"); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy preview was silently reinterpreted: %v", err)
	}
	assertRepairTimezone(t, other, "UTC")
	retentionPurge(t, previewer, scope, preview)
	assertRepairTimezone(t, previewer, "Europe/Istanbul")
}
