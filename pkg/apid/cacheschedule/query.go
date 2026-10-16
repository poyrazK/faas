package cacheschedule

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ListQuery strips page selectors before the shared strict environment resolver.
func ListQuery(r *http.Request) (*http.Request, api.EnvironmentCacheScheduleListOptions, error) {
	var o api.EnvironmentCacheScheduleListOptions
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return r, o, err
	}
	bad := errors.New("invalid schedule page selector")
	for key, values := range q {
		if len(values) != 1 {
			return r, o, bad
		}
		switch key {
		case "environment", "status", "from", "until", "after", "limit":
		default:
			return r, o, bad
		}
	}
	o.Status = q.Get("status")
	o.After = q.Get("after")
	if v, ok := q["limit"]; ok {
		o.Limit, err = strconv.Atoi(v[0])
		if err != nil || o.Limit < 1 {
			return r, o, bad
		}
	}
	for key, target := range map[string]**time.Time{"from": &o.From, "until": &o.Until} {
		if v, ok := q[key]; ok {
			at, e := time.Parse(time.RFC3339Nano, v[0])
			if e != nil {
				return r, o, bad
			}
			*target = &at
		}
	}
	o, err = api.NormalizeEnvironmentCacheScheduleList(o)
	if err != nil {
		return r, o, err
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	for _, key := range []string{"status", "from", "until", "after", "limit"} {
		q.Del(key)
	}
	clone.URL.RawQuery = q.Encode()
	return clone, o, nil
}

func ActivityQuery(r *http.Request) (*http.Request, string, int, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return r, "", 0, err
	}
	bad := errors.New("invalid schedule activity selector")
	for key, values := range q {
		if len(values) != 1 || key != "environment" && key != "after" && key != "limit" {
			return r, "", 0, bad
		}
	}
	after := q.Get("after")
	limit := 0
	if v, ok := q["limit"]; ok {
		limit, err = strconv.Atoi(v[0])
		if err != nil || limit < 1 {
			return r, "", 0, bad
		}
	}
	limit, err = api.NormalizeEnvironmentCacheActivityPage(after, limit)
	if err != nil {
		return r, "", 0, err
	}
	selected := r.Clone(r.Context())
	u := *r.URL
	selected.URL = &u
	q.Del("after")
	q.Del("limit")
	selected.URL.RawQuery = q.Encode()
	return selected, after, limit, nil
}
