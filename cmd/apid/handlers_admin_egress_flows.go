package main

import (
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	egressFlowDefaultWindow = 24 * time.Hour
	egressFlowDefaultLimit  = 200
)

// listEgressFlows is GET /v1/admin/egress-flows (ADR-369): the egress flow
// log filtered by remote address or CIDR (remote), account (account_id)
// and a [from, to) window, newest first. It answers "which tenant
// connected to <ip> at <time>" when a provider reports traffic from the
// platform's egress address.
func (s *server) listEgressFlows(w http.ResponseWriter, r *http.Request, _ state.Account) {
	filter, prob := parseEgressFlowFilter(r, timeNow().UTC())
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	logStore, ok := s.store.(state.EgressFlowLogStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("the egress flow log is not available on this store"))
		return
	}
	rows, err := logStore.ListEgressFlows(r.Context(), filter)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the egress flow log"))
		return
	}
	resp := api.EgressFlowLogResponse{Flows: make([]api.EgressFlowLogEntry, 0, len(rows)), Truncated: len(rows) >= filter.Limit}
	for _, row := range rows {
		resp.Flows = append(resp.Flows, api.EgressFlowLogEntry{
			ObservedAt: row.ObservedAt, Node: row.NodeName, AccountID: row.AccountID, AppID: row.AppID,
			InstanceID: row.InstanceID, RemoteIP: row.RemoteIP.String(), RemotePort: int(row.RemotePort),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseEgressFlowFilter(r *http.Request, now time.Time) (state.EgressFlowFilter, *api.Problem) {
	q := r.URL.Query()
	bad := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad egress flow query", detail)
	}
	f := state.EgressFlowFilter{AccountID: strings.TrimSpace(q.Get("account_id")), To: now, Limit: egressFlowDefaultLimit}
	if remote := strings.TrimSpace(q.Get("remote")); remote != "" {
		if strings.Contains(remote, "/") {
			p, err := netip.ParsePrefix(remote)
			if err != nil {
				return f, bad("remote must be an IP address or CIDR")
			}
			f.Remote = p.Masked()
		} else {
			a, err := netip.ParseAddr(remote)
			if err != nil {
				return f, bad("remote must be an IP address or CIDR")
			}
			f.Remote = netip.PrefixFrom(a, a.BitLen())
		}
	}
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"to", &f.To}, {"from", &f.From}} {
		if v := q.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return f, bad(p.name + " must be an RFC 3339 time")
			}
			*p.dst = t.UTC()
		}
	}
	if f.From.IsZero() {
		f.From = f.To.Add(-egressFlowDefaultWindow)
	}
	if !f.From.Before(f.To) {
		return f, bad("from must be before to")
	}
	if f.To.Sub(f.From) > time.Duration(api.EgressFlowLogRetentionDays+1)*24*time.Hour {
		return f, bad("the window may not exceed the flow log retention of " + strconv.Itoa(api.EgressFlowLogRetentionDays) + " days")
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > api.EgressFlowLogPageMax {
			return f, bad("limit must be between 1 and " + strconv.Itoa(api.EgressFlowLogPageMax))
		}
		f.Limit = n
	}
	return f, nil
}
