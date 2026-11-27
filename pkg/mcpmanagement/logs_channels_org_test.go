// adr: 20261119110000
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// logsFixture serves log, drain, channel and org routes on top of the
// operation fixture.
type logsFixture struct {
	*operationFixture
	lmu        sync.Mutex
	logQueries []string
	tests      int
	orgReads   []string
}

func newLogsFixture(t *testing.T, org string) (*management, *logsFixture) {
	t.Helper()
	f := &logsFixture{operationFixture: &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}}
	backend := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(backend.Close)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, agentKey: true, Toolsets: []string{ToolsetAll}})
	if err != nil {
		t.Fatal(err)
	}
	m.opts.Org = org
	return m, f
}

func (f *logsFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	appID := f.base.app.ID
	switch path := r.URL.Path; {
	case path == "/v1/apps/alpha/log-events":
		f.logQueries = append(f.logQueries, r.URL.RawQuery)
		encode(api.LogEventsResponse{})
	case path == "/v1/apps/alpha/log-drains":
		encode([]api.AppLogDrainResponse{{ID: "d-1", AppID: appID, Kind: "http", TargetURL: "https://logs.example.com/ingest?api_key=SECRET", Enabled: true}})
	case path == "/v1/apps/alpha/log-drains/d-other":
		encode(api.AppLogDrainResponse{ID: "d-other", AppID: "another-app"})
	case path == "/v1/notification-channels":
		encode([]api.NotificationChannelResponse{{ID: "ch-1", Kind: "slack", Target: "https://hooks.slack.com/services/T0/B0/SECRET", LastError: `Post "https://hooks.slack.com/services/T0/B0/SECRET": context deadline exceeded`}, {ID: "ch-2", Kind: "email", Target: "oncall@example.com"}})
	case path == "/v1/notification-channels/ch-1":
		encode(api.NotificationChannelResponse{ID: "ch-1", Kind: "slack", Target: "https://hooks.slack.com/services/T0/B0/SECRET"})
	case path == "/v1/notification-channels/ch-1/test":
		f.tests++
		encode(api.TestNotificationChannelResponse{Delivered: true})
	case path == "/v1/orgs":
		encode(api.OrgListResponse{Orgs: []api.OrgResponse{{Slug: "acme"}}})
	case path == "/v1/orgs/acme/members":
		f.orgReads = append(f.orgReads, "members")
		encode(api.MemberListResponse{Members: []api.OrgMemberResponse{{AccountID: "a-1", Role: "admin"}}})
	case path == "/v1/orgs/acme/invitations":
		f.orgReads = append(f.orgReads, "invitations")
		encode(api.InvitationListResponse{})
	default:
		f.lmu.Unlock()
		f.serveHTTP(w, r)
		f.lmu.Lock()
	}
}

func TestLogSearchBoundsFilters(t *testing.T) {
	m, f := newLogsFixture(t, "")
	if _, err := m.searchLogs(t.Context(), logSearchArgs{App: "alpha", Level: "error", Status: "5xx", Query: "timeout"}); err != nil {
		t.Fatal(err)
	}
	if len(f.logQueries) != 1 || !strings.Contains(f.logQueries[0], "since=1h") || !strings.Contains(f.logQueries[0], "q=timeout") {
		t.Fatalf("queries = %v", f.logQueries)
	}
	for _, a := range []logSearchArgs{{App: "alpha", Query: "a\nb"}, {App: "alpha", Query: strings.Repeat("x", 257)}, {App: "alpha", Limit: 101}} {
		if _, err := m.searchLogs(t.Context(), a); err == nil {
			t.Errorf("accepted %+v", a)
		}
	}
	if _, err := m.getLogFacets(t.Context(), logSearchArgs{App: "alpha", Cursor: "c"}); err == nil {
		t.Fatal("facets accepted a cursor")
	}
}

func TestDestinationsHideCredentials(t *testing.T) {
	m, _ := newLogsFixture(t, "")
	for _, call := range []func() (any, error){
		func() (any, error) { return m.listLogDrains(t.Context(), appArgs{App: "alpha"}) },
		func() (any, error) { return m.listNotificationChannels(t.Context(), struct{}{}) },
	} {
		out, err := call()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "oncall@") {
			t.Fatalf("destination leaked: %s", raw)
		}
	}
	if _, err := m.getLogDrainHealth(t.Context(), logDrainArgs{App: "alpha", Drain: "d-other"}); err == nil {
		t.Fatal("another app's drain accepted")
	}
}

func TestChannelTestIsRateLimited(t *testing.T) {
	m, f := newLogsFixture(t, "")
	if _, err := m.testNotificationChannel(t.Context(), channelArgs{Channel: "ch-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.testNotificationChannel(t.Context(), channelArgs{Channel: "ch-1"}); err == nil {
		t.Fatal("second test within a minute accepted")
	}
	if f.tests != 1 {
		t.Fatalf("tests = %d", f.tests)
	}
}

func TestOrgMembersStayInTheConnectionsOrg(t *testing.T) {
	m, f := newLogsFixture(t, "acme")
	if _, err := m.listOrgMembers(t.Context(), orgArgs{Org: "other"}); err == nil {
		t.Fatal("another organization accepted")
	}
	if _, err := m.listOrgMembers(t.Context(), orgArgs{}); err != nil {
		t.Fatal(err)
	}
	if len(f.orgReads) != 2 {
		t.Fatalf("reads = %v", f.orgReads)
	}
	m, f = newLogsFixture(t, "")
	if _, err := m.listOrgMembers(t.Context(), orgArgs{Org: "nope"}); err == nil {
		t.Fatal("organization outside membership accepted")
	}
	// A personal connection that names no organization reads the account's
	// only one; it used to be refused with "name the organization".
	if _, err := m.listOrgMembers(t.Context(), orgArgs{}); err != nil {
		t.Fatal(err)
	}
	if len(f.orgReads) != 2 {
		t.Fatalf("personal reads = %v", f.orgReads)
	}
}
