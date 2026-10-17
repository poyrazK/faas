package issuetrackers

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

type linearStoreFake struct {
	state.IssueTrackerLinearStore
	writes   int
	record   state.IssueTrackerLinearRecord
	expected int64
}

func (s *linearStoreFake) ConfigureIssueTrackerLinearConnection(_ context.Context, in state.IssueTrackerLinearRecord, expected int64) (state.IssueTrackerLinearRecord, error) {
	s.writes++
	s.record = in
	s.expected = expected
	in.Connection.Revision = expected + 1
	return in, nil
}
func TestLinearDisconnectNeedsNeitherProviderNorSealingKey(t *testing.T) {
	store := &linearStoreFake{}
	out, err := ConfigureLinearConnection(context.Background(), store, nil, nil, "account", "app", api.UpdateIssueTrackerLinearConnectionRequest{ExpectedRevision: 7})
	if err != nil || out.Connected || out.Revision != 8 || store.expected != 7 || len(store.record.SealedCredential) != 0 {
		t.Fatal("disconnect failed", err)
	}
	for _, in := range []api.UpdateIssueTrackerLinearConnectionRequest{{ExpectedRevision: -1}, {APIKey: "secret"}, {TeamID: "team"}} {
		if _, err := ConfigureLinearConnection(context.Background(), store, nil, nil, "account", "app", in); err == nil || store.writes != 1 {
			t.Fatal("invalid disconnect persisted")
		}
	}
	if _, err := ConfigureLinearConnection(context.Background(), store, nil, nil, "account", "app", api.UpdateIssueTrackerLinearConnectionRequest{Connected: true}); err == nil || store.writes != 1 {
		t.Fatal("connection without sealing key persisted")
	}
}
