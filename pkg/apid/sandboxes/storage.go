package sandboxes

// adr: 20261122132055 §8 — GET /v1/sandbox-storage shows the bytes an
// account's paused sessions kept stored, hour by hour, and their total in
// GB-hours beside the plan's free allowance. Metered dark: billable stays
// false until the GB-month price is set.

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// sandboxStorageWindowMax bounds one storage request's window.
const sandboxStorageWindowMax = 31 * 24 * time.Hour

// Storage handles GET /v1/sandbox-storage?from=&to= (RFC 3339; the current
// UTC month so far when omitted).
func (h *Handler) Storage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.configured(w) {
		return
	}
	if !h.Persistent {
		api.WriteProblem(w, api.ErrSandboxUnsupported("storage", "persistent sessions are not available on this platform yet"))
		return
	}
	from, to, prob := h.storageWindow(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	hours, err := h.Store.SandboxStorageHours(r.Context(), acct.ID, from, to)
	if err != nil {
		api.WriteProblem(w, h.problem(acct, "sandbox.storage", err))
		return
	}
	out := api.SandboxStorageUsage{From: from, To: to, Hours: make([]api.SandboxStorageHour, 0, len(hours)),
		FreeGB: api.SandboxPausedBytesFreePerAccount[acct.Plan.PlanIndex()] >> 30}
	var byteHours int64
	for _, hr := range hours {
		out.Hours = append(out.Hours, api.SandboxStorageHour{Hour: hr.Hour, PausedSessions: hr.PausedSessions,
			CaptureBytes: hr.CaptureBytes, VolumeBytes: hr.VolumeBytes})
		byteHours += hr.CaptureBytes + hr.VolumeBytes
	}
	out.GBHours = float64(byteHours) / float64(1<<30)
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// storageWindow reads from and to, defaulting to the current UTC month so
// far, and bounds the window.
func (h *Handler) storageWindow(r *http.Request) (time.Time, time.Time, *api.Problem) {
	now := h.now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := now
	q := r.URL.Query()
	for _, f := range []struct {
		name string
		into *time.Time
	}{{"from", &from}, {"to", &to}} {
		raw := q.Get(f.name)
		if raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, to, api.ErrValidation(f.name + " must be an RFC 3339 time")
		}
		*f.into = t.UTC()
	}
	switch {
	case !from.Before(to):
		return from, to, api.ErrValidation("from must be before to")
	case to.Sub(from) > sandboxStorageWindowMax:
		return from, to, api.ErrValidation("the window is at most 31 days")
	}
	return from, to, nil
}
