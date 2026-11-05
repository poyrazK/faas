package main

// ADR-1163: live job task output. GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/logs/stream
// follows the task VM's console ring on its node's vmmd and renders the same
// SSE frames as the app log stream. Like the ADR-1037 attach route it is
// compute-owned: apid never dials vmmd.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

const (
	jobTaskLogsPoll      = time.Second
	jobTaskLogsHeartbeat = 15 * time.Second
	jobTaskLogsBackstop  = 3 * time.Hour
	jobTaskLogsDial      = 10 * time.Second

	jobTaskLogsEndFinished    = api.JobTaskLogsEndFinished
	jobTaskLogsEndTimeout     = api.JobTaskLogsEndTimeout
	jobTaskLogsEndUnreachable = api.JobTaskLogsEndUnreachable
	jobTaskLogsEndGone        = api.JobTaskLogsEndNotFound
	jobTaskLogsEndStored      = api.JobTaskLogsEndStoredOutput
)

type jobTaskLogTargets interface {
	JobTaskLogTarget(ctx context.Context, accountID, jobName, runID string, taskIndex int) (state.JobTaskLogTarget, error)
}

// JobTaskLogsHandler streams one job task's output while it runs.
type JobTaskLogsHandler struct {
	Auth    *mwauth.Middleware
	Targets jobTaskLogTargets
	Nodes   appTaskAttachNodes
	Log     *slog.Logger
	// Traffic enrolls the stream under the account's security scope
	// (admitPlatformTraffic); nil only in legacy fixtures.
	Traffic *trafficrevocation.Registry
	// Poll, Heartbeat and Backstop default to the constants above; tests
	// shorten them.
	Poll      time.Duration
	Heartbeat time.Duration
	Backstop  time.Duration
}

func (h *JobTaskLogsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	inner := mwauth.AccountHandler(h.stream)
	chain := h.Auth.RequireScope(api.ScopesReadSurface...)(h.Auth.RequireMFA(inner))
	h.Auth.RequireLimited(chain)(w, r)
}

// jobTaskLogsRequest is a validated stream request.
type jobTaskLogsRequest struct {
	account, job, run string
	index             int
	sinceSeq          int64
}

func (h *JobTaskLogsHandler) stream(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.JobsAllowed() {
		api.WriteProblem(w, api.ErrPlanJobsNotAllowed(acct.Plan))
		return
	}
	req, prob := parseJobTaskLogsRequest(r, acct)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	target, err := h.Targets.JobTaskLogTarget(r.Context(), req.account, req.job, req.run, req.index)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeJobTaskNotFound,
			"Task not found", "no task with this index exists for the job run"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the task"))
		return
	}
	ctx, release, ok := admitPlatformTraffic(r.Context(), w, r, h.Traffic, w.Header(), acct.ID, "")
	if !ok {
		return
	}
	defer release()
	apislogs.StartSSE(w)
	flusher, _ := w.(http.Flusher)
	reason := h.follow(ctx, w, flusher, req, target)
	_, _ = fmt.Fprintf(w, "event: end\ndata: {\"reason\":%q}\n\n", reason)
	if flusher != nil {
		flusher.Flush()
	}
}

func parseJobTaskLogsRequest(r *http.Request, acct state.Account) (jobTaskLogsRequest, *api.Problem) {
	index, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil || index < 0 {
		return jobTaskLogsRequest{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid task_index", "task_index must be a non-negative integer")
	}
	var since int64
	if v := r.URL.Query().Get("since_seq"); v != "" {
		if since, err = strconv.ParseInt(v, 10, 64); err != nil || since < 0 {
			return jobTaskLogsRequest{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid since_seq", "since_seq must be a non-negative integer")
		}
	}
	return jobTaskLogsRequest{account: acct.ID, job: r.PathValue("name"), run: r.PathValue("id"), index: index, sinceSeq: since}, nil
}

// follow streams every attempt's VM output until the task is terminal, and
// returns the end reason. A VM's ring appears only once vmmd boots it, so a
// missing ring before any line arrived is retried; a broken stream resumes
// after the last relayed line.
func (h *JobTaskLogsHandler) follow(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, req jobTaskLogsRequest, target state.JobTaskLogTarget) string {
	ctx, cancel := context.WithTimeout(ctx, durationOr(h.Backstop, jobTaskLogsBackstop))
	defer cancel()
	next := map[string]int64{} // instance -> next sequence to request
	closed := map[string]bool{}
	firstInstance := ""
	for {
		id := target.InstanceID
		if id != "" && target.NodeID != "" && !closed[id] {
			if firstInstance == "" {
				firstInstance = id
				next[id] = req.sinceSeq // a client cursor applies to the attempt it came from
			}
			res := h.followInstance(ctx, w, flusher, target, next[id])
			if res.done {
				return res.reason
			}
			if res.lastSeq > 0 {
				next[id] = res.lastSeq + 1
			}
			closed[id] = res.closed
		}
		if target.Terminal() {
			if id != "" && closed[id] {
				return jobTaskLogsEndFinished
			}
			// The last VM was gone before its output could be relayed.
			return jobTaskLogsEndStored
		}
		if !h.wait(ctx, w, flusher) {
			return jobTaskLogsEndTimeout
		}
		reloaded, err := h.Targets.JobTaskLogTarget(ctx, req.account, req.job, req.run, req.index)
		switch {
		case errors.Is(err, state.ErrNotFound):
			return jobTaskLogsEndGone
		case err != nil:
			h.logWarn("job task logs: reload task", err)
			continue
		}
		target = reloaded
	}
}

// instanceFollow is the outcome of relaying one VM's ring.
type instanceFollow struct {
	// lastSeq is the last sequence relayed, 0 when none was.
	lastSeq int64
	// closed is true when the ring ended: the VM is gone and every retained
	// line was relayed.
	closed bool
	// done ends the whole stream with reason (client gone, backstop,
	// unreachable node).
	done   bool
	reason string
}

// followInstance relays one VM's ring from since, with heartbeats while the
// task is quiet.
func (h *JobTaskLogsHandler) followInstance(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, target state.JobTaskLogTarget, since int64) instanceFollow {
	dialCtx, cancelDial := context.WithTimeout(ctx, jobTaskLogsDial)
	client, closer, ok := h.Nodes.ClientFor(dialCtx, target.NodeID)
	cancelDial()
	if !ok {
		return instanceFollow{done: true, reason: jobTaskLogsEndUnreachable}
	}
	defer func() { _ = closer.Close() }()
	if since <= 0 {
		since = 1 // replay the ring from its first line
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var res instanceFollow
	stream, err := client.Logs(streamCtx, &vmmdpb.LogsRequest{Instance: target.InstanceID, SinceSeq: since, Follow: proto.Bool(true)})
	if err != nil {
		return h.instanceStreamEnd(ctx, res, err)
	}
	lines := make(chan *vmmdpb.LogsResponse, 64)
	recvErr := make(chan error, 1)
	go func() {
		for {
			line, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case lines <- line:
			case <-streamCtx.Done():
				return
			}
		}
	}()
	relay := func(line *vmmdpb.LogsResponse) {
		renderJobTaskLogFrame(w, flusher, target.InstanceID, line)
		if !line.GetIsGap() && line.GetSeq() > res.lastSeq {
			res.lastSeq = line.GetSeq()
		}
	}
	heartbeat := time.NewTicker(durationOr(h.Heartbeat, jobTaskLogsHeartbeat))
	defer heartbeat.Stop()
	for {
		select {
		case line := <-lines:
			relay(line)
		case err := <-recvErr:
			for drained := false; !drained; {
				select {
				case line := <-lines:
					relay(line)
				default:
					drained = true
				}
			}
			return h.instanceStreamEnd(ctx, res, err)
		case <-heartbeat.C:
			writeSSEComment(w, flusher)
		case <-ctx.Done():
			return instanceFollow{done: true, reason: jobTaskLogsEndTimeout}
		}
	}
}

// instanceStreamEnd classifies how one VM's stream stopped. EOF means vmmd
// closed the ring with the VM. NotFound before any line means the VM has not
// booted yet (retry); after a line it means the ring is already gone. Any
// other error is retried from the last relayed line.
func (h *JobTaskLogsHandler) instanceStreamEnd(ctx context.Context, res instanceFollow, err error) instanceFollow {
	switch {
	case ctx.Err() != nil:
		return instanceFollow{done: true, reason: jobTaskLogsEndTimeout}
	case errors.Is(err, io.EOF):
		res.closed = true
	case status.Code(err) == codes.NotFound:
		res.closed = res.lastSeq > 0
	default:
		h.logWarn("job task logs: vmmd stream", err)
	}
	return res
}

func renderJobTaskLogFrame(w http.ResponseWriter, flusher http.Flusher, instanceID string, line *vmmdpb.LogsResponse) {
	frame := scheddgrpc.LogFrame{
		InstanceID: instanceID, Seq: line.GetSeq(), Stream: line.GetStream(), Line: line.GetLine(),
		Level: line.GetLevel(), WrittenAt: line.GetWrittenAt().AsTime(),
		IsGap: line.GetIsGap(), GapToWrittenAt: line.GetGapToWrittenAt().AsTime(), GapReason: line.GetGapReason(),
	}
	if frame.IsGap {
		apislogs.RenderAppLogGap(w, flusher, frame, "", nil)
		return
	}
	apislogs.RenderAppLogEvent(w, flusher, frame, "", nil)
}

func writeSSEComment(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = fmt.Fprint(w, ":\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// wait sleeps one poll interval, then sends a heartbeat comment.
func (h *JobTaskLogsHandler) wait(ctx context.Context, w http.ResponseWriter, flusher http.Flusher) bool {
	timer := time.NewTimer(durationOr(h.Poll, jobTaskLogsPoll))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	writeSSEComment(w, flusher)
	return true
}

func (h *JobTaskLogsHandler) logWarn(msg string, err error) {
	if h.Log != nil {
		h.Log.Warn(msg, "err", err)
	}
}

func durationOr(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

// jobTaskLogsCarveOut serves the stream route ahead of the apid proxy.
type jobTaskLogsCarveOut struct {
	logs http.Handler
	next http.Handler
}

func (c jobTaskLogsCarveOut) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.logs != nil && r.Method == http.MethodGet && api.IsJobTaskLogStreamPath(r.URL.Path) {
		c.logs.ServeHTTP(w, r)
		return
	}
	c.next.ServeHTTP(w, r)
}
