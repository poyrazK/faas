package vmmdgrpc

// Persisted sandbox workspaces (ADR-20261027090013 step 7). A persist
// archives the session's workspace into the artifact store: vmmd asks
// agentd, on the session's RunSandbox stream, for a reservation within the
// account's snapshot quota, streams the guest's archive to the reserved key,
// and answers once agentd has recorded the snapshot, so a snapshot ID the
// caller holds is always usable. A session started from a snapshot gets the
// archive unpacked into its workspace before it is reported booted.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/storage"
)

const (
	// sandboxGrantTimeout bounds agentd's answer to a reservation, and
	// sandboxRecordTimeout its record of a written archive.
	sandboxGrantTimeout  = 30 * time.Second
	sandboxRecordTimeout = 30 * time.Second
	// sandboxArchiveDeleteTimeout bounds the delete of an archive that was
	// not recorded.
	sandboxArchiveDeleteTimeout = 30 * time.Second
)

// WithSandboxWorkspaces lets sessions persist their workspaces to, and
// start from, the artifact store. Without it PersistSandboxWorkspace is
// Unimplemented and a session from a snapshot fails to boot.
func (s *Server) WithSandboxWorkspaces(backend storage.StorageBackend) *Server {
	s.sandboxStorage = backend
	return s
}

// sandboxPersists pairs a persist's requests with agentd's answers.
type sandboxPersists struct {
	mu      sync.Mutex
	waiting map[string]chan *vmmdpb.RunSandboxRequest
}

func (p *sandboxPersists) await(requestID string) (<-chan *vmmdpb.RunSandboxRequest, func()) {
	ch := make(chan *vmmdpb.RunSandboxRequest, 1)
	p.mu.Lock()
	if p.waiting == nil {
		p.waiting = map[string]chan *vmmdpb.RunSandboxRequest{}
	}
	p.waiting[requestID] = ch
	p.mu.Unlock()
	return ch, func() {
		p.mu.Lock()
		delete(p.waiting, requestID)
		p.mu.Unlock()
	}
}

// deliver hands an answer to the persist waiting for it; an answer nobody
// waits for any more is dropped.
func (p *sandboxPersists) deliver(requestID string, msg *vmmdpb.RunSandboxRequest) {
	p.mu.Lock()
	ch := p.waiting[requestID]
	p.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

// exchange sends ev on the session's stream and waits for the answer to
// requestID.
func (a *sandboxActivity) exchange(ctx context.Context, requestID string, ev *vmmdpb.RunSandboxEvent, timeout time.Duration) (*vmmdpb.RunSandboxRequest, error) {
	answer, stop := a.persists.await(requestID)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case a.outbox <- ev:
	case <-a.done:
		return nil, errSandboxStreamEnded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case msg := <-answer:
		return msg, nil
	case <-a.done:
		return nil, errSandboxStreamEnded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var errSandboxStreamEnded = errors.New("the sandbox session ended")

// PersistSandboxWorkspace archives a session's workspace to the artifact
// store and returns the recorded snapshot.
func (s *Server) PersistSandboxWorkspace(ctx context.Context, req *vmmdpb.PersistSandboxWorkspaceRequest) (*vmmdpb.PersistSandboxWorkspaceResponse, error) {
	if s.sandboxStorage == nil {
		return nil, status.Error(codes.Unimplemented, "this vmmd persists no sandbox workspaces")
	}
	instance, sandboxID := req.GetInstance(), req.GetSandboxId()
	if instance == "" || sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "instance and sandbox are required")
	}
	path := req.GetPath()
	if path == "" {
		path = "."
	}
	probe := sandboxproto.Request{Op: sandboxproto.OpArchiveCreate, Path: path, MaxBytes: 1}
	if err := probe.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if _, inside := sandboxproto.WorkspacePath("/workspace", path); !inside {
		return nil, status.Error(codes.InvalidArgument, "path must be inside the workspace")
	}
	act := s.sandboxActivityFor(instance)
	if act == nil || act.sandboxID != sandboxID {
		return nil, status.Error(codes.NotFound, "no such sandbox session")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.SandboxPersistTimeoutSec)*time.Second)
	defer cancel()
	s.beginActivity(instance)
	defer s.endActivity(instance)
	act.begin()
	defer func() { act.end(time.Now()) }()
	if err := s.thawSandbox(ctx, instance, act); err != nil {
		s.log.Warn("sandbox: thaw failed", "instance", instance, "err", err)
		return nil, status.Error(codes.Unavailable, "the sandbox could not be thawed; retry")
	}
	if err := s.prepareGuest(ctx, instance); err != nil {
		return nil, status.Error(codes.Unavailable, "the sandbox is frozen or resuming; retry")
	}
	grant, err := s.persistGrant(ctx, act)
	if err != nil {
		return nil, err
	}
	result, err := s.writeWorkspaceArchive(ctx, instance, sandboxID, path, grant)
	if err != nil {
		s.persistFailed(ctx, act, grant)
		return nil, err
	}
	if err := s.persistRecord(ctx, act, grant, result); err != nil {
		return nil, err
	}
	return &vmmdpb.PersistSandboxWorkspaceResponse{SnapshotId: grant.GetSnapshotId(), Bytes: result.Bytes, Sha256: result.SHA256,
		Entries: int64(result.Entries), ExpiresAtUnixMs: grant.GetExpiresAtUnixMs()}, nil
}

// persistGrant asks agentd for a reservation; a refusal is agentd's
// problem, which reaches the caller as it is.
func (s *Server) persistGrant(ctx context.Context, act *sandboxActivity) (*vmmdpb.RunSandboxPersistGrant, error) {
	requestID := uuid.NewString()
	msg, err := act.exchange(ctx, requestID, &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_PersistRequested{
		PersistRequested: &vmmdpb.RunSandboxPersistRequested{RequestId: requestID}}}, sandboxGrantTimeout)
	if err != nil {
		return nil, sandboxExchangeStatus(err)
	}
	grant := msg.GetPersistGrant()
	if grant == nil {
		return nil, status.Error(codes.Internal, "agentd answered a persist with no grant")
	}
	if refusal := grant.GetRefusal(); len(refusal) > 0 {
		var problem api.Problem
		if err := json.Unmarshal(refusal, &problem); err != nil || problem.Code == "" {
			return nil, status.Error(codes.Internal, "agentd refused the persist unreadably")
		}
		return nil, grpcerr.ToStatus(&problem)
	}
	if grant.GetSnapshotId() == "" || grant.GetStorageKey() == "" || grant.GetMaxBytes() < 1 {
		return nil, status.Error(codes.Internal, "agentd granted an incomplete reservation")
	}
	grant.RequestId = requestID
	return grant, nil
}

// writeWorkspaceArchive streams the guest's archive of path into the
// granted key. A failure deletes whatever was written.
func (s *Server) writeWorkspaceArchive(ctx context.Context, instance, sandboxID, path string, grant *vmmdpb.RunSandboxPersistGrant) (sandboxproto.ArchiveResult, error) {
	sessions, ok := s.vmm.(sandboxSessions)
	if !ok {
		return sandboxproto.ArchiveResult{}, status.Error(codes.Unimplemented, "this vmmd serves no sandbox sessions")
	}
	conn, err := sessions.DialSandboxSession(ctx, instance, sandboxID)
	if errors.Is(err, fcvm.ErrSandboxNotFound) {
		return sandboxproto.ArchiveResult{}, status.Error(codes.NotFound, "no such sandbox session")
	}
	if err != nil {
		return sandboxproto.ArchiveResult{}, status.Error(codes.Unavailable, "the sandbox session is unreachable")
	}
	pr, pw := io.Pipe()
	stored := make(chan error, 1)
	go func() {
		err := s.sandboxStorage.Put(ctx, grant.GetStorageKey(), pr)
		_ = pr.CloseWithError(err)
		stored <- err
	}()
	var result sandboxproto.ArchiveResult
	req := sandboxproto.Request{Op: sandboxproto.OpArchiveCreate, Path: path, MaxBytes: grant.GetMaxBytes()}
	guestErr := sandboxproto.Do(ctx, conn, req, func(_ uint32, body []byte) error {
		_, err := pw.Write(body)
		return err
	}, &result)
	_ = pw.CloseWithError(guestErr)
	storeErr := <-stored
	if guestErr == nil && storeErr == nil {
		return result, nil
	}
	s.deleteWorkspaceArchive(ctx, grant.GetStorageKey())
	var refused *sandboxproto.GuestError
	switch {
	case errors.As(guestErr, &refused):
		return sandboxproto.ArchiveResult{}, grpcerr.ToStatus(sandboxproto.Problem(sandboxproto.ErrorFrame{Code: refused.Code, Message: refused.Message}))
	case guestErr != nil && ctx.Err() == nil && storeErr == nil:
		return sandboxproto.ArchiveResult{}, status.Error(codes.Unavailable, "the sandbox session closed before the archive was written")
	case ctx.Err() != nil:
		return sandboxproto.ArchiveResult{}, status.FromContextError(ctx.Err()).Err()
	}
	s.log.Warn("sandbox: workspace archive write failed", "instance", instance, "err", storeErr)
	return sandboxproto.ArchiveResult{}, status.Error(codes.Unavailable, "the workspace archive could not be stored; retry")
}

// persistFailed tells agentd the reservation went unused, so it drops it.
func (s *Server) persistFailed(ctx context.Context, act *sandboxActivity, grant *vmmdpb.RunSandboxPersistGrant) {
	done := &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_PersistDone{PersistDone: &vmmdpb.RunSandboxPersistDone{
		RequestId: grant.GetRequestId(), SnapshotId: grant.GetSnapshotId(), Failed: true}}}
	// The answer is not needed: agentd's sweep drops an unanswered
	// reservation anyway.
	_, _ = act.exchange(context.WithoutCancel(ctx), grant.GetRequestId(), done, sandboxRecordTimeout)
}

// persistRecord reports the written archive and waits for agentd's record;
// an archive agentd did not record is deleted.
func (s *Server) persistRecord(ctx context.Context, act *sandboxActivity, grant *vmmdpb.RunSandboxPersistGrant, result sandboxproto.ArchiveResult) error {
	done := &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_PersistDone{PersistDone: &vmmdpb.RunSandboxPersistDone{
		RequestId: grant.GetRequestId(), SnapshotId: grant.GetSnapshotId(), Bytes: result.Bytes, Sha256: result.SHA256}}}
	msg, err := act.exchange(ctx, grant.GetRequestId(), done, sandboxRecordTimeout)
	if err == nil && msg.GetPersistRecorded().GetRecorded() {
		return nil
	}
	s.deleteWorkspaceArchive(ctx, grant.GetStorageKey())
	if err != nil {
		return sandboxExchangeStatus(err)
	}
	return grpcerr.ToStatus(api.NewProblem(http.StatusServiceUnavailable, api.CodeSandboxWorkspaceUnavailable, "Workspace not persisted",
		"the snapshot could not be recorded; persist again"))
}

func (s *Server) deleteWorkspaceArchive(ctx context.Context, key string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxArchiveDeleteTimeout)
	defer cancel()
	if err := s.sandboxStorage.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
		s.log.Warn("sandbox: delete unrecorded workspace archive failed", "key", key, "err", err)
	}
}

func sandboxExchangeStatus(err error) error {
	switch {
	case errors.Is(err, errSandboxStreamEnded):
		return status.Error(codes.NotFound, "the sandbox session ended")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return status.Error(codes.Unavailable, "agentd did not answer the persist; retry")
	}
	return status.Error(codes.Unavailable, err.Error())
}

// hydrateSandboxWorkspace unpacks a persisted workspace into a booted
// session's /workspace. Its problem fails the session's start.
func (s *Server) hydrateSandboxWorkspace(ctx context.Context, wake fcvm.SandboxWakeRequest, key string) *api.Problem {
	unavailable := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusConflict, api.CodeSandboxWorkspaceUnavailable, "Workspace unavailable", detail)
	}
	sessions, ok := s.vmm.(sandboxSessions)
	if s.sandboxStorage == nil || !ok {
		return unavailable("this node cannot restore persisted workspaces")
	}
	if !strings.HasPrefix(key, "agents/workspaces/"+wake.AccountID+"/") {
		return unavailable("the workspace archive is not the session account's")
	}
	s.beginActivity(wake.Instance)
	defer s.endActivity(wake.Instance)
	if err := s.prepareGuest(ctx, wake.Instance); err != nil {
		return unavailable("the session's VM is not running")
	}
	archive, err := s.sandboxStorage.Get(ctx, key)
	if err != nil {
		s.log.Warn("sandbox: read workspace archive failed", "instance", wake.Instance, "err", err)
		return unavailable("the persisted workspace could not be read")
	}
	defer func() { _ = archive.Close() }()
	conn, err := sessions.DialSandboxSession(ctx, wake.Instance, wake.SandboxID)
	if err != nil {
		return unavailable("the session's VM is unreachable")
	}
	req := sandboxproto.Request{Op: sandboxproto.OpArchiveExtract, Path: ".",
		MaxBytes: int64(api.SandboxWorkspaceMaxMB[wake.Plan.PlanIndex()]) << 20, MaxEntries: api.SandboxArchiveEntriesMax}
	client, err := sandboxproto.Start(ctx, conn, req)
	if err != nil {
		_ = conn.Close()
		return unavailable("the persisted workspace could not be unpacked")
	}
	defer func() { _ = client.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	uploaded := make(chan error, 1)
	go func() { uploaded <- client.Upload(ctx, archive) }()
	_, waitErr := client.Wait(ctx, nil)
	_ = client.Close()
	uploadErr := <-uploaded
	if waitErr != nil || uploadErr != nil {
		s.log.Warn("sandbox: unpack workspace archive failed", "instance", wake.Instance, "err", errors.Join(waitErr, uploadErr))
		return unavailable(fmt.Sprintf("the persisted workspace could not be unpacked: %s", hydrateCause(waitErr)))
	}
	return nil
}

// hydrateCause names a guest refusal of the archive, and nothing else.
func hydrateCause(err error) string {
	var refused *sandboxproto.GuestError
	if errors.As(err, &refused) && refused.Code != sandboxproto.CodeInternal {
		return refused.Code
	}
	return "the session failed"
}
