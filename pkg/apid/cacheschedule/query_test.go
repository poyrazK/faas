package cacheschedule

import (
	"net/http/httptest"
	"testing"
)

func TestListQuerySelectors(t *testing.T) {
	for _, query := range []string{"environment=staging", "environment=staging&status=scheduled&limit=1&from=2026-10-15T00:00:00Z&until=2026-10-16T00:00:00Z&after=opaque"} {
		r := httptest.NewRequest("GET", "/cache/schedules?"+query, nil)
		selected, o, e := ListQuery(r)
		if e != nil || selected.URL.RawQuery != "environment=staging" || r.URL.RawQuery != query || o.Limit < 1 {
			t.Fatal("selector mutation", selected, o, e)
		}
	}
	for _, query := range []string{"limit=0", "limit=51", "limit=1&limit=2", "status=unknown", "until=bad", "from=2026-10-16T00:00:00Z&until=2026-10-15T00:00:00Z", "unknown=1", "environment=staging&environment=staging", "bad=%zz"} {
		r := httptest.NewRequest("GET", "/cache/schedules?"+query, nil)
		if _, _, e := ListQuery(r); e == nil {
			t.Fatal("invalid selector accepted", query)
		}
	}
}

func TestActivityQuerySelectors(t *testing.T) {
	for _, query := range []string{"environment=staging", "environment=staging&after=opaque&limit=1"} {
		r := httptest.NewRequest("GET", "/cache/schedules/activity?"+query, nil)
		selected, _, limit, e := ActivityQuery(r)
		if e != nil || selected.URL.RawQuery != "environment=staging" || r.URL.RawQuery != query || limit < 1 {
			t.Fatal(selected, limit, e)
		}
	}
	for _, query := range []string{"limit=0", "limit=51", "limit=1&limit=2", "after=a&after=b", "environment=staging&environment=staging", "status=failed", "from=2026-10-15T00:00:00Z", "bad=%zz"} {
		if _, _, _, e := ActivityQuery(httptest.NewRequest("GET", "/cache/schedules/activity?"+query, nil)); e == nil {
			t.Fatal("invalid activity selector", query)
		}
	}
}
