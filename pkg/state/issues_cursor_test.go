package state_test

import (
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
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

func TestIssueTagCursorBindsFilterAndWindow(t *testing.T) {
	now := time.Now().UTC()
	end := now.Add(-time.Minute)
	cursor := state.IssueCursor{Time: now, ID: uuid.NewString(), Sort: "recent", Tag: "component=payments", ImpactWindowEnd: &end}
	decoded, err := state.DecodeIssueCursor(state.EncodeIssueCursor(cursor))
	if err != nil {
		t.Fatal(err)
	}
	if err = state.ValidateIssueListCursor(state.IssueListFilter{Tag: "component=payments"}, decoded); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"", "component=exports", "email=private@example.com"} {
		if state.ValidateIssueListCursor(state.IssueListFilter{Tag: tag}, decoded) == nil {
			t.Fatal("changed filter accepted")
		}
	}
	cursor.Tag = "email=private@example.com"
	if _, err = state.DecodeIssueCursor(state.EncodeIssueCursor(cursor)); err == nil {
		t.Fatal("unknown tag accepted")
	}
}

func TestIssueTagCursorAcceptsMaximumEscapedValue(t *testing.T) {
	now := time.Now().UTC()
	end := now.Add(-time.Minute)
	tag := "component=" + strings.Repeat("<", api.IssueTagValueMaxBytes)
	cursor := state.IssueCursor{Time: now, ID: uuid.NewString(), Sort: "impact", Tag: tag, ImpactWindowEnd: &end, MinCustomers: 9223372036854775807, ImpactCustomers: 9223372036854775807}
	encoded := state.EncodeIssueCursor(cursor)
	if len(encoded) > api.IssueCursorMaxBytes {
		t.Fatal("encoder produced a cursor the decoder rejects")
	}
	decoded, err := state.DecodeIssueCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err = state.ValidateIssueListCursor(state.IssueListFilter{Sort: "impact", Tag: tag, MinCustomers: cursor.MinCustomers}, decoded); err != nil {
		t.Fatal(err)
	}
}
