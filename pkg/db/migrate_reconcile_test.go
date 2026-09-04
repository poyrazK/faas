package db

import (
	"testing"

	"github.com/pressly/goose/v3"
)

func migration(version int64, source string) *goose.Migration {
	return &goose.Migration{Version: version, Source: source}
}

func TestMigrationOptionsForMissingReservations(t *testing.T) {
	tests := []struct {
		name       string
		current    int64
		known      map[int64]struct{}
		found      goose.Migrations
		wantOption bool
		wantRepair []int64
	}{
		{
			name:    "missing reservation is repairable",
			current: 578,
			known:   map[int64]struct{}{562: {}, 564: {}},
			found: goose.Migrations{
				migration(562, "00562_mail_suppressions.sql"),
				migration(563, "00563_reserve_slot.sql"),
				migration(564, "00564_reserve_slot.sql"),
			},
			wantOption: true,
			wantRepair: []int64{563},
		},
		{
			name:    "multiple missing reservations are repairable",
			current: 590,
			known:   map[int64]struct{}{562: {}, 564: {}},
			found: goose.Migrations{
				migration(563, "00563_reserve_slot.sql"),
				migration(565, "00565_slot_reservation.sql"),
			},
			wantOption: true,
			wantRepair: []int64{563, 565},
		},
		{
			name:    "real migration keeps strict mode",
			current: 578,
			known:   map[int64]struct{}{562: {}},
			found: goose.Migrations{
				migration(563, "00563_upload_sessions.sql"),
			},
			wantOption: false,
			wantRepair: nil,
		},
		{
			name:    "known reservation needs no repair",
			current: 578,
			known:   map[int64]struct{}{563: {}},
			found: goose.Migrations{
				migration(563, "00563_reserve_slot.sql"),
			},
			wantOption: false,
			wantRepair: nil,
		},
		{
			name:    "current version is not historical gap",
			current: 563,
			known:   map[int64]struct{}{},
			found: goose.Migrations{
				migration(563, "00563_reserve_slot.sql"),
			},
			wantOption: false,
			wantRepair: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			option, repaired := migrationOptionsForMissingReservations(tt.current, tt.known, tt.found)
			if (option != nil) != tt.wantOption {
				t.Fatalf("option present = %v, want %v", option != nil, tt.wantOption)
			}
			if len(repaired) != len(tt.wantRepair) {
				t.Fatalf("repaired = %v, want %v", repaired, tt.wantRepair)
			}
			for i := range repaired {
				if repaired[i] != tt.wantRepair[i] {
					t.Errorf("repaired[%d] = %d, want %d", i, repaired[i], tt.wantRepair[i])
				}
			}
		})
	}
}

func TestIsReservationMigrationSource(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{source: "00563_reserve_slot.sql", want: true},
		{source: "00565_slot_reservation.sql", want: true},
		{source: "00566_no_op_slot_reservation.sql", want: true},
		{source: "nested/00567_RESERVE_SLOT.sql", want: true},
		{source: "00563_upload_sessions.sql", want: false},
		{source: "00563_reserve_slot.go", want: false},
		{source: "563_reserve_slot.sql", want: false},
	}

	for _, tt := range tests {
		if got := isReservationMigrationSource(tt.source); got != tt.want {
			t.Errorf("isReservationMigrationSource(%q) = %v, want %v", tt.source, got, tt.want)
		}
	}
}
