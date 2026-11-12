package agentundo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// planned is one record with its status in a plan.
type planned struct {
	rec    state.AgentUndoRecord
	inv    *Inverse
	chk    *Check
	item   api.AgentUndoItem
	forced bool
}

// Plan lists the key's changes req selects, newest first, with what undoing
// each will do. acct is the person (or agent) who will apply it.
func (e *Engine) Plan(ctx context.Context, base *http.Request, acct state.Account, keyID string, req api.AgentUndoRequest) (api.AgentUndoPlan, *api.Problem) {
	since, items, problem := e.plan(ctx, base, acct, keyID, req)
	if problem != nil {
		return api.AgentUndoPlan{}, problem
	}
	out := api.AgentUndoPlan{Since: since.Format(time.RFC3339), Items: make([]api.AgentUndoItem, 0, len(items))}
	for _, p := range items {
		out.Items = append(out.Items, p.item)
		if p.item.Status == api.AgentUndoUndoable || p.forced {
			out.Undoable++
		}
	}
	out.PlanDigest = digest(items, req.Force)
	return out, nil
}

// Apply undoes the changes of a reviewed plan: req must carry the digest
// Plan returned for the same request. Changes are undone newest first; a
// change that cannot be undone leaves the older changes of its resource.
func (e *Engine) Apply(ctx context.Context, base *http.Request, acct state.Account, keyID string, req api.AgentUndoRequest) (api.AgentUndoResult, *api.Problem) {
	_, items, problem := e.plan(ctx, base, acct, keyID, req)
	if problem != nil {
		return api.AgentUndoResult{}, problem
	}
	if req.PlanDigest == "" || req.PlanDigest != digest(items, req.Force) {
		return api.AgentUndoResult{}, api.NewProblem(http.StatusConflict, "undo_plan_changed", "Undo plan changed",
			"the agent's changes or their state changed since the plan was made; plan the undo again and review it")
	}
	out := api.AgentUndoResult{Items: make([]api.AgentUndoItem, 0, len(items))}
	blocked := map[string]bool{}
	for _, p := range items {
		item := p.item
		if item.Status == api.AgentUndoChangedSince && !p.forced {
			// Left as it is; older changes of the resource stay too.
			blocked[p.resourceKey()] = true
			item.Result = api.AgentUndoResultChangedSince
		}
		if item.Status == api.AgentUndoUndoable || p.forced {
			e.applyOne(ctx, base, acct, p, req.Force, blocked, &item)
			switch item.Result {
			case api.AgentUndoResultUndone:
				out.Undone++
			case api.AgentUndoResultFailed:
				out.Failed++
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// resourceKey groups the records of one resource.
func (p planned) resourceKey() string {
	if r := p.chk.resource(); r != "" {
		return r
	}
	if p.inv != nil {
		return p.inv.Path
	}
	return p.rec.ID
}

func (e *Engine) applyOne(ctx context.Context, base *http.Request, acct state.Account, p planned, force bool, blocked map[string]bool, item *api.AgentUndoItem) {
	resource := p.resourceKey()
	if blocked[resource] {
		item.Result = api.AgentUndoResultSkipped
		item.Reason = "a newer change to the same resource was not undone"
		return
	}
	same, revision := e.check(ctx, base, acct, p.chk)
	if !same && !force {
		blocked[resource] = true
		item.Result, item.Status = api.AgentUndoResultChangedSince, api.AgentUndoChangedSince
		item.Reason = "changed again since the agent's change; undo with force to overwrite it"
		return
	}
	body, err := inverseBody(p.inv, revision)
	if err != nil {
		blocked[resource] = true
		item.Result, item.ErrorCode, item.Error = api.AgentUndoResultFailed, "undo_failed", "the stored undo could not be read"
		return
	}
	status, answer := e.Dispatch(ctx, base, acct, p.inv.Method, p.inv.Path, body)
	if !is2xx(status) && !(status == http.StatusNotFound && p.inv.GoneIsDone) {
		blocked[resource] = true
		item.Result = api.AgentUndoResultFailed
		item.ErrorCode, item.Error = problemOf(status, answer)
		return
	}
	if _, err := e.Store.MarkAgentUndone(ctx, p.rec.ID, acct.ID); err != nil {
		item.Result, item.ErrorCode, item.Error = api.AgentUndoResultFailed, "undo_not_recorded", "the change was undone but could not be marked undone"
		return
	}
	item.Result, item.Status = api.AgentUndoResultUndone, api.AgentUndoUndone
}

// inverseBody fills the expected revision and a fresh request id.
func inverseBody(inv *Inverse, revision json.RawMessage) ([]byte, error) {
	if len(inv.Body) == 0 && revision == nil && !inv.NewRequestID {
		return nil, nil
	}
	body := map[string]json.RawMessage{}
	if len(inv.Body) > 0 {
		if err := json.Unmarshal(inv.Body, &body); err != nil {
			return nil, err
		}
	}
	if revision != nil {
		body["expected_revision"] = revision
	}
	if inv.NewRequestID {
		body["request_id"] = mustJSON(uuid.NewString())
	}
	return json.Marshal(body)
}

func (e *Engine) plan(ctx context.Context, base *http.Request, acct state.Account, keyID string, req api.AgentUndoRequest) (time.Time, []planned, *api.Problem) {
	now := e.now()
	oldest := now.Add(-api.AgentActivityRetention)
	since := now.Add(-api.DefaultAgentUndoWindow)
	if len(req.Entries) > 0 {
		since = oldest
	}
	if req.Since != "" {
		t, err := time.Parse(time.RFC3339, req.Since)
		if err != nil {
			return time.Time{}, nil, api.ErrValidation("since must be an RFC 3339 time")
		}
		since = t.UTC()
	}
	if since.Before(oldest) {
		since = oldest
	}
	records, err := e.Store.AgentUndoRecords(ctx, keyID, since, api.MaxAgentUndoRecords+1)
	if err != nil {
		return time.Time{}, nil, api.ErrCapacity("could not read the agent's changes")
	}
	if len(req.Entries) > 0 {
		records, err = pick(records, req.Entries)
		if err != nil {
			return time.Time{}, nil, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Change not found", err.Error())
		}
	}
	if len(records) > api.MaxAgentUndoRecords {
		return time.Time{}, nil, api.NewProblem(http.StatusUnprocessableEntity, "undo_too_many_changes", "Too many changes",
			"the agent made more than "+strconv.Itoa(api.MaxAgentUndoRecords)+" changes in this window; choose a later since or name entries")
	}
	items := make([]planned, 0, len(records))
	checked := map[string]bool{}
	for _, rec := range records {
		p := e.status(ctx, base, acct, rec, checked)
		p.forced = req.Force && p.item.Status == api.AgentUndoChangedSince
		items = append(items, p)
	}
	return since, items, nil
}

// status reads a record into a plan item. Only the newest undoable record of
// a resource is checked against the resource now: older ones are checked
// when apply reaches them, after the newer ones were undone.
func (e *Engine) status(ctx context.Context, base *http.Request, acct state.Account, rec state.AgentUndoRecord, checked map[string]bool) planned {
	p := planned{rec: rec, item: api.AgentUndoItem{
		ActivityID: rec.ActivityID, Method: rec.Method, Path: rec.Path, Kind: rec.Kind, Summary: rec.Summary,
		CreatedAt: rec.CreatedAt.UTC().Format(time.RFC3339), Status: api.AgentUndoUndoable,
	}}
	switch {
	case rec.UndoneAt != nil:
		p.item.Status = api.AgentUndoUndone
		return p
	case len(rec.Inverse) == 0:
		p.item.Status, p.item.Reason = api.AgentUndoNotUndoable, rec.NotUndoableReason
		return p
	}
	p.inv = &Inverse{}
	if json.Unmarshal(rec.Inverse, p.inv) != nil {
		p.item.Status, p.item.Reason = api.AgentUndoNotUndoable, "the stored undo could not be read"
		return p
	}
	if len(rec.Guard) > 0 {
		p.chk = &Check{}
		if json.Unmarshal(rec.Guard, p.chk) != nil {
			p.item.Status, p.item.Reason = api.AgentUndoNotUndoable, "the stored undo could not be read"
			return p
		}
	}
	resource := p.chk.resource()
	if resource == "" || checked[resource] {
		return p
	}
	checked[resource] = true
	if same, _ := e.check(ctx, base, acct, p.chk); !same {
		p.item.Status = api.AgentUndoChangedSince
		p.item.Reason = "changed again since the agent's change"
	}
	return p
}

// check reports whether the resource is still as the agent left it, and the
// revision an inverse should expect.
func (e *Engine) check(ctx context.Context, base *http.Request, acct state.Account, chk *Check) (bool, json.RawMessage) {
	if chk == nil {
		return true, nil
	}
	switch chk.Kind {
	case CheckFields:
		now, ok := e.getObject(ctx, base, acct, chk.Path)
		if !ok {
			return false, nil
		}
		var revision json.RawMessage
		if chk.RevisionField != "" {
			revision = now[chk.RevisionField]
		}
		fields := now
		if chk.Object != "" {
			if json.Unmarshal(now[chk.Object], &fields) != nil {
				return false, revision
			}
		}
		for k, want := range chk.Fields {
			if !sameJSON(valueOrNull(fields, k), want) {
				return false, revision
			}
		}
		return true, revision
	case CheckConfig:
		newest, ok := e.newestRevision(ctx, base, acct, chk.Path)
		if !ok || newest == 0 {
			return false, nil
		}
		revision := mustJSON(newest)
		if newest == chk.AfterRevision {
			return true, revision
		}
		return e.sameVariables(ctx, base, acct, chk.Path, newest, chk.AfterRevision), revision
	}
	return false, nil
}

// sameVariables compares two revisions' variables in memory; values are
// never kept.
func (e *Engine) sameVariables(ctx context.Context, base *http.Request, acct state.Account, revisions string, a, b int64) bool {
	read := func(n int64) (map[string]string, bool) {
		status, body := e.Dispatch(ctx, base, acct, http.MethodGet, revisionDetailPath(revisions, n), nil)
		var detail api.ConfigRevisionDetail
		if status != http.StatusOK || json.Unmarshal(body, &detail) != nil {
			return nil, false
		}
		return detail.Variables, true
	}
	va, okA := read(a)
	vb, okB := read(b)
	return okA && okB && reflect.DeepEqual(va, vb)
}

func sameJSON(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// pick keeps the records of the named activity entries.
func pick(records []state.AgentUndoRecord, entries []string) ([]state.AgentUndoRecord, error) {
	out := make([]state.AgentUndoRecord, 0, len(entries))
	var missing []string
	for _, id := range entries {
		i := slices.IndexFunc(records, func(r state.AgentUndoRecord) bool { return r.ActivityID == id })
		if i < 0 {
			missing = append(missing, id)
			continue
		}
		if !slices.ContainsFunc(out, func(r state.AgentUndoRecord) bool { return r.ID == records[i].ID }) {
			out = append(out, records[i])
		}
	}
	if len(missing) > 0 {
		return nil, notFoundError("no undoable record of these activity entries in the last 7 days: " + strings.Join(missing, ", "))
	}
	slices.SortStableFunc(out, func(a, b state.AgentUndoRecord) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out, nil
}

type notFoundError string

func (e notFoundError) Error() string { return string(e) }

// digest binds a plan: which records, in which state, and whether forced.
func digest(items []planned, force bool) string {
	h := sha256.New()
	if force {
		h.Write([]byte("force\n"))
	}
	for _, p := range items {
		h.Write([]byte(p.rec.ID + "|" + p.item.Status + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
