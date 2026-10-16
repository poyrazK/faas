package environmentgitops

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type retirementJournalTestStore struct {
	row                  state.EnvironmentWorkloadRetirement
	reconciled, accepted int
}

func (s *retirementJournalTestStore) BeginEnvironmentGitOpsWorkloadRetirements(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) ([]state.EnvironmentGitOpsStep, error) {
	return nil, state.ErrConflict
}
func (s *retirementJournalTestStore) ActivateEnvironmentGitOpsEmptyRetirement(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) (bool, error) {
	return false, state.ErrConflict
}
func (s *retirementJournalTestStore) PendingEnvironmentGitOpsWorkloadRetirements(context.Context, state.EnvironmentGitOpsLease, []string) ([]state.EnvironmentWorkloadRetirement, error) {
	r := s.row
	r.AcknowledgedGateways = slices.Clone(r.AcknowledgedGateways)
	return []state.EnvironmentWorkloadRetirement{r}, nil
}
func (s *retirementJournalTestStore) AcknowledgeEnvironmentGitOpsWorkloadRetirement(_ context.Context, _ state.EnvironmentGitOpsLease, id string, generation int64, node string) error {
	if id != s.row.ID || generation != s.row.RouteGeneration || !slices.Contains(s.row.ExpectedGateways, node) {
		return state.ErrConflict
	}
	s.accepted++
	if !slices.Contains(s.row.AcknowledgedGateways, node) {
		s.row.AcknowledgedGateways = append(s.row.AcknowledgedGateways, node)
		slices.Sort(s.row.AcknowledgedGateways)
	}
	return nil
}
func (s *retirementJournalTestStore) ReconcileEnvironmentGitOpsWorkloadRetirements(context.Context, state.EnvironmentGitOpsLease) (bool, error) {
	s.reconciled++
	return slices.Equal(s.row.ExpectedGateways, s.row.AcknowledgedGateways), nil
}
func (s *retirementJournalTestStore) ListEnvironmentGitOpsWorkloadRetirements(context.Context, string, string) ([]state.EnvironmentWorkloadRetirement, error) {
	return []state.EnvironmentWorkloadRetirement{s.row}, nil
}

type retirementTestNotifier struct {
	t             *testing.T
	events        chan db.Notification
	deliver, sent bool
	published     int
}

func (n *retirementTestNotifier) Subscribe(context.Context, []string) (<-chan db.Notification, func(), error) {
	return n.events, func() {}, nil
}
func (n *retirementTestNotifier) Notify(_ context.Context, channel, payload string) error {
	n.published++
	var route db.DeploymentRouteChangedPayload
	if channel != db.NotifyDeploymentRouteChanged || json.Unmarshal([]byte(payload), &route) != nil || route.AppID != "original-app" || route.DeploymentID != "original-deployment" || route.Generation != 7 {
		n.t.Fatalf("withdrawal changed original route identity: %s %s", channel, payload)
	}
	if n.deliver && !n.sent {
		n.sent = true
		for _, ack := range []db.DeploymentRouteAckPayload{{Generation: 6, Node: "edge-a"}, {Generation: 7, Node: "foreign"}, {Generation: 7, Node: "edge-a"}} {
			raw, _ := json.Marshal(ack)
			n.events <- db.Notification{Channel: db.NotifyDeploymentRouteAck, Payload: string(raw)}
		}
	}
	return nil
}
func TestWorkloadRetirementControllerRestartsFromPendingWithdrawal(t *testing.T) {
	journal := &retirementJournalTestStore{row: state.EnvironmentWorkloadRetirement{ID: "retirement", AppID: "original-app", Phase: "retiring", RouteDeploymentID: "original-deployment", RouteGeneration: 7, ExpectedGateways: []string{"edge-a"}}}
	notifier := &retirementTestNotifier{t: t, events: make(chan db.Notification, 4)}
	ready, err := ReconcileWorkloadRetirements(t.Context(), journal, notifier, state.EnvironmentGitOpsLease{}, []string{"edge-a"}, 20*time.Millisecond, 5*time.Millisecond)
	if err != nil || ready || journal.reconciled != 0 || journal.accepted != 0 || notifier.published == 0 {
		t.Fatalf("timeout certified withdrawal: ready=%t err=%v journal=%+v", ready, err, journal)
	}
	restarted := &retirementTestNotifier{t: t, events: make(chan db.Notification, 4), deliver: true}
	ready, err = ReconcileWorkloadRetirements(t.Context(), journal, restarted, state.EnvironmentGitOpsLease{}, []string{"edge-a"}, time.Second, 10*time.Millisecond)
	if err != nil || !ready || journal.accepted != 1 || journal.reconciled != 1 {
		t.Fatalf("restart/stale acknowledgements: ready=%t err=%v journal=%+v", ready, err, journal)
	}
}
