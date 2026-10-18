package operatorobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261015173127929: cursors pin episode/revision, while evidence has one atomic triage snapshot.
func TestTriageHistoryCursorAndEvidence(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	snapshot, err := alertSnapshot(historyRules(now, true), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	episode := snapshot.Episodes[0]
	actor := "11111111-1111-4111-8111-111111111111"
	trace := strings.Repeat("d", 32)
	for i, action := range []string{"acknowledge", "claim", "release"} {
		if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: actor, Action: action, ExpectedRevision: int64(i), Now: now, TraceID: &trace}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := TriageHistory(ctx, store, episode.SeriesID, "", 1, now)
	if err != nil || first.IncidentID != "platform_alert/"+episode.ID || first.NextCursor == "" || first.Items[0].Revision != 3 || first.Items[0].TracePath != "/v1/admin/obs/traces/"+trace {
		t.Fatalf("first: %+v %v", first, err)
	}
	if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: actor, Action: "unacknowledge", ExpectedRevision: 3, Now: now}); err != nil {
		t.Fatal(err)
	}
	second, err := TriageHistory(ctx, store, episode.ID, first.NextCursor, 2, now.Add(time.Second))
	if err != nil || second.SnapshotRevision != 3 || second.CurrentRevision != 4 || second.Truncated || len(second.Items) != 2 || second.Items[0].Revision != 2 || !second.SnapshotAt.Equal(first.SnapshotAt) {
		t.Fatalf("second: %+v %v", second, err)
	}
	evidence, err := Evidence(ctx, store, nil, "platform_alert", episode.ID, 1, now)
	if err != nil || evidence.TriageHistory == nil || evidence.TriageHistory.SnapshotRevision != evidence.Incident.Triage.Revision || len(evidence.History) != 1 || evidence.History[0].Kind != "platform_alert.firing" || evidence.Incident.Status != "firing" {
		t.Fatalf("evidence: %+v %v", evidence, err)
	}
	body, err := json.Marshal(evidence.TriageHistory)
	if err != nil || strings.Contains(string(body), actor) || strings.Contains(string(body), "owner_account_id") {
		t.Fatalf("unsafe history: %s %v", body, err)
	}
	if _, err := TriageHistory(ctx, store, episode.ID, first.NextCursor, 1, now.Add(api.ObsAlertTriageHistoryCursorTTL+time.Second)); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("expired cursor: %v", err)
	}
	key, err := cursor.Decode(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"garbage", strings.Repeat("x", api.ObsAlertTriageHistoryCursorMaxBytes+1), cursor.Encode(cursor.Key{CreatedAt: key.CreatedAt, ID: "different/v1/" + episode.ID + "|3|2"}), cursor.Encode(cursor.Key{CreatedAt: key.CreatedAt, ID: "triage-history/v1/" + episode.ID + "|3|4"})} {
		if _, err := TriageHistory(ctx, store, episode.ID, bad, 1, now); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("bad cursor %q: %v", bad, err)
		}
	}
	recovered, err := alertSnapshot(historyRules(now.Add(time.Second), false), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertSnapshot(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	recurring, err := alertSnapshot(historyRules(now.Add(2*time.Second), true), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertSnapshot(ctx, recurring); err != nil {
		t.Fatal(err)
	}
	if _, err := TriageHistory(ctx, store, episode.SeriesID, first.NextCursor, 1, now.Add(2*time.Second)); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("cursor crossed recurrence: %v", err)
	}
}
