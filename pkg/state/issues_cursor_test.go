package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestIssueListImpactCursorKeepsWindowAndRejectsChangedQuery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	windowEnd := now.Add(-time.Minute)
	id := uuid.NewString()
	legacy := state.IssueCursor{Time: now, ID: id}
	encoded := state.EncodeIssueCursor(legacy)
	decoded, err := state.DecodeIssueCursor(encoded)
	if err != nil || decoded.Time != legacy.Time || decoded.ID != legacy.ID {
		t.Fatalf("legacy cursor round trip = %+v, %v", decoded, err)
	}
	if err := state.ValidateIssueListCursor(state.IssueListFilter{Sort: "recent"}, decoded); err != nil {
		t.Fatalf("legacy recent cursor rejected: %v", err)
	}

	impact := state.IssueCursor{
		Time: now, ID: id, Sort: "impact", MinCustomers: 2, ImpactCustomers: 7, ImpactWindowEnd: &windowEnd,
	}
	encoded = state.EncodeIssueCursor(impact)
	decoded, err = state.DecodeIssueCursor(encoded)
	if err != nil || decoded.Sort != "impact" || decoded.MinCustomers != 2 || decoded.ImpactCustomers != 7 || decoded.ImpactWindowEnd == nil || !decoded.ImpactWindowEnd.Equal(windowEnd) {
		t.Fatalf("impact cursor round trip = %+v, %v", decoded, err)
	}
	if err := state.ValidateIssueListCursor(state.IssueListFilter{Sort: "impact", MinCustomers: 2}, decoded); err != nil {
		t.Fatalf("matching impact cursor rejected: %v", err)
	}
	if err := state.ValidateIssueListCursor(state.IssueListFilter{Sort: "impact", MinCustomers: 3}, decoded); err == nil {
		t.Fatal("impact cursor reused with a changed customer threshold")
	}
	if err := state.ValidateIssueListCursor(state.IssueListFilter{Sort: "recent", MinCustomers: 2}, decoded); err == nil {
		t.Fatal("impact cursor reused with recent ordering")
	}
}

func TestDecodeIssueCursorRejectsInvalidImpactMetadata(t *testing.T) {
	now := time.Now().UTC()
	windowEnd := now.Add(-time.Minute)
	for _, cursor := range []state.IssueCursor{
		{Time: now, ID: uuid.NewString(), Sort: "popular", ImpactWindowEnd: &windowEnd},
		{Time: now, ID: uuid.NewString(), Sort: "impact", MinCustomers: -1, ImpactWindowEnd: &windowEnd},
		{Time: now, ID: uuid.NewString(), Sort: "impact", ImpactCustomers: -1, ImpactWindowEnd: &windowEnd},
		{Time: now, ID: uuid.NewString(), Sort: "impact"},
	} {
		if _, err := state.DecodeIssueCursor(state.EncodeIssueCursor(cursor)); err == nil {
			t.Fatalf("invalid impact cursor accepted: %+v", cursor)
		}
	}
}
