package buildlog_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/buildlog"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type newestDeployments struct {
	ids   []string
	err   error
	calls int
	limit int
}

func (n *newestDeployments) ListDeploymentsForApp(_ context.Context, _ string, limit, _ int) ([]state.Deployment, error) {
	n.calls++
	n.limit = limit
	out := make([]state.Deployment, 0, len(n.ids))
	for _, id := range n.ids {
		out = append(out, state.Deployment{ID: id})
	}
	return out, n.err
}

// adr: 20261102182856
func TestExpiredMatchesTheRetentionPolicy(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -meter.BuildLogRetentionDays-1)
	recent := now.AddDate(0, 0, -meter.BuildLogRetentionDays+1)
	built := func(id string, status state.DeploymentStatus, created time.Time) state.Deployment {
		return state.Deployment{ID: id, AppID: "app", Kind: state.DeploymentKindTarball, Status: status, CreatedAt: created}
	}
	cases := []struct {
		name    string
		d       state.Deployment
		newest  []string
		want    bool
		listing bool
	}{
		{"old and pushed out", built("d1", state.DeployFailed, old), []string{"d9"}, true, true},
		{"old but among the newest", built("d1", state.DeploySuperseded, old), []string{"d9", "d1"}, false, true},
		{"live", built("d1", state.DeployLive, old), nil, false, false},
		{"inside the window", built("d1", state.DeployFailed, recent), nil, false, false},
		{"image deployments have no build log", state.Deployment{ID: "d1", Kind: state.DeploymentKindImage, Status: state.DeploySuperseded, CreatedAt: old}, nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lister := &newestDeployments{ids: tc.newest}
			got, err := buildlog.Expired(context.Background(), lister, tc.d, now)
			if err != nil || got != tc.want {
				t.Fatalf("Expired = %v, %v; want %v", got, err, tc.want)
			}
			if (lister.calls > 0) != tc.listing {
				t.Fatalf("listed newest deployments %d times", lister.calls)
			}
			if tc.listing && lister.limit != api.BuildLogRetainedDeployments {
				t.Fatalf("listed %d newest deployments, want %d", lister.limit, api.BuildLogRetainedDeployments)
			}
		})
	}
	boom := errors.New("database unavailable")
	if _, err := buildlog.Expired(context.Background(), &newestDeployments{err: boom}, built("d1", state.DeployFailed, old), now); !errors.Is(err, boom) {
		t.Fatalf("list failure = %v", err)
	}
}

func TestNoticeStatesThePolicy(t *testing.T) {
	n := buildlog.Notice()
	if n.RetentionDays != meter.BuildLogRetentionDays || n.RetainedDeployments != api.BuildLogRetainedDeployments ||
		!strings.Contains(n.Message, "expired") || !strings.Contains(n.Message, "90 days") || !strings.Contains(n.Message, "10 most recent") {
		t.Fatalf("notice = %+v", n)
	}
}
