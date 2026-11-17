package sandboxdata

// Workspace archives (ADR-20261027090013 step 7). hydrate unpacks an
// uploaded tar.gz into the workspace, within the plan's
// SandboxWorkspaceMaxMB. persist has the session's node archive the
// workspace into Gregale's artifact store, within the account's
// SandboxSnapshotBytesPerAccount, and answers with the recorded snapshot,
// which a new session can start from.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

// sandboxWorkspace is the guest's workspace root.
const sandboxWorkspace = "/workspace"

// workspaceCaps refuses a token minted before tokens carried the workspace
// cap; reading the session again mints one that does.
func workspaceCaps(w http.ResponseWriter, c sandboxtoken.Claims) bool {
	if c.WorkspaceMaxBytes > 0 {
		return true
	}
	w.Header().Set("Retry-After", "1")
	api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeSandboxNotReady, "Sandbox token out of date",
		"read the session again for a token that carries its workspace limits"))
	return false
}

func (h *Handler) workspaceHydrate(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok || !workspaceCaps(w, c) {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	req := sandboxproto.Request{Op: sandboxproto.OpArchiveExtract, Path: path, MaxBytes: c.WorkspaceMaxBytes,
		MaxEntries: api.SandboxArchiveEntriesMax}
	body := http.MaxBytesReader(w, r.Body, sandboxproto.MaxArchiveBytes)
	raw, prob := h.call(r.Context(), c, req, body, nil)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	var result sandboxproto.ArchiveResult
	if err := sandboxproto.DecodeStrict(raw, &result); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, api.CodeInternal, "Sandbox failed", "the session sent an unreadable result"))
		return
	}
	writeJSON(w, http.StatusOK, api.HydrateSandboxWorkspaceResponse{Bytes: result.Bytes, Entries: int64(result.Entries)})
}

func (h *Handler) workspacePersist(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok {
		return
	}
	var body api.PersistSandboxWorkspaceRequest
	if r.ContentLength != 0 {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, requestMaxBytes))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			api.WriteProblem(w, api.ErrValidation("the body must be a JSON object with an optional path"))
			return
		}
	}
	resp, prob := h.persist(r.Context(), c, body.Path)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	writeJSON(w, http.StatusCreated, api.PersistSandboxWorkspaceResponse{SnapshotID: resp.GetSnapshotId(), Bytes: resp.GetBytes(),
		SHA256: resp.GetSha256(), Entries: resp.GetEntries(), ExpiresAt: time.UnixMilli(resp.GetExpiresAtUnixMs()).UTC()})
}

// persist asks the session's node to archive its workspace.
func (h *Handler) persist(ctx context.Context, c sandboxtoken.Claims, path string) (*vmmdpb.PersistSandboxWorkspaceResponse, *api.Problem) {
	if path != "" {
		probe := sandboxproto.Request{Op: sandboxproto.OpArchiveCreate, Path: path, MaxBytes: 1}
		if err := probe.Validate(); err != nil {
			return nil, api.ErrValidation(strings.TrimPrefix(err.Error(), sandboxproto.ErrInvalidRequest.Error()+": "))
		}
		// Refused before anything is reserved; the guest checks again.
		if _, inside := sandboxproto.WorkspacePath(sandboxWorkspace, path); !inside {
			return nil, api.NewProblem(http.StatusForbidden, api.CodeSandboxOutsideWorkspace, "Sandbox refused the call", "path must be inside the workspace")
		}
	}
	dialCtx, cancel := context.WithTimeout(ctx, nodeDialTimeout)
	client, closer, ok := h.Nodes.ClientFor(dialCtx, c.NodeID)
	cancel()
	if !ok {
		return nil, api.NewProblem(http.StatusServiceUnavailable, api.CodeInternal, "Node unreachable", "the node running this session is not reachable")
	}
	defer func() { _ = closer.Close() }()
	resp, err := client.PersistSandboxWorkspace(ctx, &vmmdpb.PersistSandboxWorkspaceRequest{Instance: c.InstanceID, SandboxId: c.SandboxID, Path: path})
	if err != nil {
		return nil, persistProblem(err)
	}
	return resp, nil
}

// persistProblem maps a persist failure: a problem vmmd or agentd raised
// keeps its code, and the guest's refusals their status.
func persistProblem(err error) *api.Problem {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument, codes.Canceled, codes.DeadlineExceeded, codes.Unimplemented:
		return callProblem(err)
	}
	p, ok := grpcerr.FromStatus(err)
	if !ok || p == nil {
		return callProblem(err)
	}
	switch p.Code {
	case api.CodeAgentLimit:
		p.Status = http.StatusForbidden
	case api.CodeSandboxUnsupported:
		p.Status = http.StatusUnprocessableEntity
	case api.CodeSandboxWorkspaceUnavailable:
		p.Status = http.StatusServiceUnavailable
	default:
		st, known := sandboxproto.ProblemStatus(p.Code)
		if !known {
			return callProblem(err)
		}
		p.Status = st
	}
	return p
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("encode the sandbox answer"))
		return
	}
	writeResult(w, code, raw, nil)
}
