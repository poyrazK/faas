package datarepair

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
)

// RehearsalExecutor is available only on trusted coordinator bindings. Source
// credentials remain in the executor; only a consistent dump leaves it.
type RehearsalExecutor interface {
	Snapshot(context.Context, core.Scope, string, string) (core.RehearsalInput, error)
	RecordRehearsal(context.Context, core.Scope, string, string, core.Evidence) (core.Plan, error)
}

func (h *Handler) snapshot(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	if !h.Coordinator || h.Exporter == nil {
		problem(w, core.ErrNotFound)
		return
	}
	in, ok := decode(w, r)
	if !ok {
		return
	}
	if in.Actor != "" || in.PlanID != "" || in.OperationID != "" || in.RepairOperationID != "" {
		problem(w, core.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	plan, err := h.Engine.Get(ctx, p.Scope, r.PathValue("id"))
	if err != nil {
		problem(w, err)
		return
	}
	if plan.Digest != in.Digest {
		problem(w, core.ErrConflict)
		return
	}
	input, err := h.Exporter.ExportWithChecks(ctx, plan, h.Engine.CheckSQL, h.Engine.Checks)
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.gregale.repair-snapshot")
	_ = core.WriteRehearsalInput(w, input)
}

func (h *Handler) recordRehearsal(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	if !h.Coordinator {
		problem(w, core.ErrNotFound)
		return
	}
	var in struct {
		Digest   string        `json:"digest"`
		Evidence core.Evidence `json:"evidence"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(api.DataRepairMaxRequestBytes)))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || in.Evidence.Runner != core.MicroVMRunner ||
		!validUUID(in.Evidence.TaskID) || !validUUID(in.Evidence.NodeID) || in.Evidence.RetiredAt == nil || in.Evidence.RunnerSHA256 == "" {
		problem(w, core.ErrInvalid)
		return
	}
	out, err := h.Engine.RecordRehearsal(r.Context(), p.Scope, r.PathValue("id"), in.Digest, in.Evidence)
	write(w, out, err)
}

func (c *Remote) Snapshot(ctx context.Context, s core.Scope, id, digest string) (core.RehearsalInput, error) {
	if s.Validate() != nil || !validUUID(id) {
		return core.RehearsalInput{}, core.ErrInvalid
	}
	body, _ := json.Marshal(request{Digest: digest})
	req, err := http.NewRequestWithContext(ctx, "POST", c.origin+"/v1/data-repair/plans/"+id+"/snapshot", bytes.NewReader(body))
	if err != nil {
		return core.RehearsalInput{}, core.ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return core.RehearsalInput{}, core.ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return core.RehearsalInput{}, core.ErrUnavailable
	}
	in, err := core.ReadRehearsalInput(resp.Body)
	if err != nil || in.Plan.Scope != s || in.Plan.ID != id || in.Plan.Digest != digest {
		return core.RehearsalInput{}, core.ErrUnavailable
	}
	return in, nil
}

func (c *Remote) RecordRehearsal(ctx context.Context, s core.Scope, id, digest string, e core.Evidence) (core.Plan, error) {
	return c.call(ctx, s, "POST", "/"+id+"/rehearsal-evidence", struct {
		Digest   string        `json:"digest"`
		Evidence core.Evidence `json:"evidence"`
	}{digest, e})
}
