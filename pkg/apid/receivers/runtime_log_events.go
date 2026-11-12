// apid-side receiver for runtime log projections (ADR-20261018083536 slice B).
//
// Direction: vmmd → apid RecordRuntimeLogEvents on the LogEvents service,
// registered beside SpansWriter on the listeners vmmd already dials for guest
// traces. vmmd classifies lines and enforces the per-app budget; apid derives
// the account from the app, caps each account across nodes, redacts the line
// and inserts a source=runtime ledger row. apid stays the only writer
// (ADR-213). On the shared mTLS listener a line is accepted only for an
// instance that runs on the node the caller's certificate names (ADR-958's
// deferred binding), so no fleet certificate can write another node's apps'
// logs.

package receivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/redact"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Outcome labels for apid_runtime_log_events_total.
const (
	runtimeLogInserted    = "inserted"
	runtimeLogRejected    = "rejected"
	runtimeLogRateLimited = "rate_limited"
	runtimeLogDBError     = "db_error"
	runtimeLogForeignNode = "foreign_node"
	runtimeLogNotReleased = "not_released"
)

// runtimeLogIDMaxBytes is the ledger's instance_id and source_event_id bound.
const runtimeLogIDMaxBytes = 256

// RuntimeLogEventsStore is the Store subset the receiver needs.
type RuntimeLogEventsStore interface {
	AppByID(ctx context.Context, id string) (state.App, error)
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
	InsertLogEvent(ctx context.Context, event state.LogEvent) (state.LogEvent, error)
}

// migrationInstanceStore also reads an instance's previous node, so lines a
// source node sends while its instance migrates are still its own.
type migrationInstanceStore interface {
	MigrationInstanceByID(ctx context.Context, id string) (state.Instance, error)
}

// RuntimeLogGate decides which runtime lines are indexed (ADR-20261024222102),
// since indexing lines nobody can search is cost without value. Active is the
// operator switch (FAAS_LOG_SEARCH_ENABLED): while it reports false the RPC
// answers Unavailable and vmmd pauses. Released decides an app's account by
// the log-search capability flag; lines of an account log search is not
// released to are counted and dropped. A nil func allows.
type RuntimeLogGate struct {
	Active   func() bool
	Released func(ctx context.Context, app state.App) bool
}

// RuntimeLogEventsReceiver is process-wide: both listeners that carry
// SpansWriter register the same receiver, so one limiter covers the account.
type RuntimeLogEventsReceiver struct {
	apidpb.UnimplementedLogEventsServer
	store    RuntimeLogEventsStore
	gate     RuntimeLogGate
	limiter  *peraccount.Limiter
	redactor *redact.Redactor
	outcomes *prometheus.CounterVec
	log      *slog.Logger
	nodes    wire.NodeIdentityResolver
	now      func() time.Time // nil = time.Now

	// marked is the minute each app last got its account-budget line, so
	// one line per app and minute is written however many are dropped.
	markMu sync.Mutex
	marked map[string]int64
}

// runtimeLogApp is one app's account and release decision, looked up once
// per batch.
type runtimeLogApp struct {
	accountID string // "" when the app is unknown
	released  bool
}

// NewRuntimeLogEventsReceiver builds the receiver. reg may be nil in tests.
func NewRuntimeLogEventsReceiver(store RuntimeLogEventsStore, gate RuntimeLogGate, reg prometheus.Registerer, log *slog.Logger) *RuntimeLogEventsReceiver {
	if log == nil {
		log = slog.Default()
	}
	r := &RuntimeLogEventsReceiver{
		store: store, gate: gate, limiter: peraccount.NewLimiter(),
		redactor: redact.New(api.RuntimeLogEventMessageMaxBytes), log: log,
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "apid_runtime_log_events_total",
			Help: "Runtime log lines vmmd forwarded to the log_events ledger, by outcome (ADR-20261018083536).",
		}, []string{"outcome"}),
	}
	if reg != nil {
		reg.MustRegister(r.outcomes)
	}
	return r
}

// WithPeerNodes binds mutual-TLS callers to the compute node their
// certificate names (multi-box apid's node verifier). Without it, or on a
// local socket, lines are accepted for any app as before.
func (r *RuntimeLogEventsReceiver) WithPeerNodes(nodes wire.NodeIdentityResolver) *RuntimeLogEventsReceiver {
	if r != nil {
		r.nodes = nodes
	}
	return r
}

// RegisterRuntimeLogEventsReceiver binds the receiver onto a gRPC server; a
// nil receiver leaves the service unregistered (vmmd sees Unimplemented and
// pauses, as it does for Unavailable).
func RegisterRuntimeLogEventsReceiver(s *grpc.Server, r *RuntimeLogEventsReceiver) {
	if r != nil {
		apidpb.RegisterLogEventsServer(s, r)
	}
}

// RecordRuntimeLogEvents implements the LogEvents RPC. Invalid and
// rate-limited lines are counted, not returned, so one bad line never drops
// its batch; a database failure fails the batch so vmmd pauses.
func (r *RuntimeLogEventsReceiver) RecordRuntimeLogEvents(ctx context.Context, req *apidpb.RecordRuntimeLogEventsRequest) (*apidpb.RecordRuntimeLogEventsResponse, error) {
	if r.gate.Active != nil && !r.gate.Active() {
		return nil, status.Error(codes.Unavailable, "runtime log events are disabled")
	}
	events := req.GetEvents()
	if len(events) > api.RuntimeLogEventsBatchMax {
		return nil, status.Errorf(codes.InvalidArgument, "batch of %d exceeds %d events", len(events), api.RuntimeLogEventsBatchMax)
	}
	node, err := r.peerNode(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "runtime log events: %v", err)
	}
	resp := &apidpb.RecordRuntimeLogEventsResponse{}
	apps := map[string]runtimeLogApp{} // app id → account and release decision
	onNode := map[string]bool{}        // instance/app → runs on the caller's node
	for _, ev := range events {
		outcome := r.record(ctx, ev, apps, node, onNode)
		r.outcomes.WithLabelValues(outcome).Inc()
		switch outcome {
		case runtimeLogInserted:
			resp.Inserted++
		case runtimeLogRateLimited:
			resp.RateLimited++
		case runtimeLogDBError:
			// A later replay of the same lines converges on the rows already
			// inserted through source_event_id.
			return nil, status.Error(codes.Unavailable, "log ledger unavailable")
		default:
			resp.Rejected++
		}
	}
	return resp, nil
}

func (r *RuntimeLogEventsReceiver) record(ctx context.Context, ev *apidpb.RuntimeLogEvent, apps map[string]runtimeLogApp, node string, onNode map[string]bool) string {
	event, ok := runtimeLogEvent(ev)
	if !ok {
		return runtimeLogRejected
	}
	if node != "" {
		own, err := r.instanceOnNode(ctx, event, node, onNode)
		if err != nil {
			r.log.Warn("runtime log events: instance lookup failed", "instance_id", event.InstanceID, "err", err)
			return runtimeLogDBError
		}
		if !own {
			return runtimeLogForeignNode
		}
	}
	decided, seen := apps[event.AppID]
	if !seen {
		app, err := r.store.AppByID(ctx, event.AppID)
		switch {
		case err == nil:
			decided = runtimeLogApp{accountID: app.AccountID, released: r.gate.Released == nil || r.gate.Released(ctx, app)}
		case errors.Is(err, state.ErrNotFound):
		default:
			r.log.Warn("runtime log events: app lookup failed", "app_id", event.AppID, "err", err)
			return runtimeLogDBError
		}
		apps[event.AppID] = decided
	}
	account, err := uuid.Parse(decided.accountID)
	if err != nil {
		return runtimeLogRejected
	}
	if !decided.released {
		return runtimeLogNotReleased
	}
	accountID := decided.accountID
	if taken, _ := r.limiter.Take(account, api.RuntimeLogEventsPerAccountMinute); !taken {
		r.markAccountBudget(ctx, event, accountID)
		return runtimeLogRateLimited
	}
	event.AccountID = accountID
	message, redactions := r.redactor.Apply(event.Message)
	event.Message = message
	if len(redactions) > 0 {
		event.Fields = redactionFields(redactions)
	}
	if _, err := r.store.InsertLogEvent(ctx, event); err != nil {
		r.log.Warn("runtime log events: insert failed", "app_id", event.AppID, "err", err)
		return runtimeLogDBError
	}
	return runtimeLogInserted
}

// markAccountBudget writes the one system line per app and minute that
// tells a searcher the account's index budget dropped lines, as vmmd's
// per-app budget does (ADR-20261104142830); without it the drops were
// silent and a log_search_matches count looked complete. The line bypasses
// the budget it reports, and its source_event_id makes another apid's copy
// of the same minute a no-op.
func (r *RuntimeLogEventsReceiver) markAccountBudget(ctx context.Context, event state.LogEvent, accountID string) {
	now := time.Now
	if r.now != nil {
		now = r.now
	}
	minute := now().UTC().Unix() / 60
	r.markMu.Lock()
	if r.marked == nil {
		r.marked = map[string]int64{}
	}
	if r.marked[event.AppID] == minute {
		r.markMu.Unlock()
		return
	}
	for app, m := range r.marked {
		if m < minute {
			delete(r.marked, app)
		}
	}
	r.marked[event.AppID] = minute
	r.markMu.Unlock()
	mark := state.LogEvent{
		OccurredAt: time.Unix((minute+1)*60, 0).Add(-time.Millisecond).UTC(),
		AccountID:  accountID, AppID: event.AppID, DeploymentID: event.DeploymentID, InstanceID: event.InstanceID,
		Source: state.LogEventSourceRuntime, SourceEventID: "account-budget:" + event.AppID + ":" + strconv.FormatInt(minute, 10),
		Level: "warn", Stream: "system",
		Message: fmt.Sprintf("this account's runtime lines this minute exceeded its %d-line search index budget; later ones were not indexed, and gregale logs still has them",
			api.RuntimeLogEventsPerAccountMinute),
	}
	if _, err := r.store.InsertLogEvent(ctx, mark); err != nil {
		r.log.Warn("runtime log events: record the account budget line", "app_id", event.AppID, "err", err)
	}
}

// peerNode is the compute node a mutual-TLS caller's certificate names, or
// "" on a local socket or without a node resolver (single box).
func (r *RuntimeLogEventsReceiver) peerNode(ctx context.Context) (string, error) {
	if r.nodes == nil {
		return "", nil
	}
	cn, err := wire.PeerCN(ctx)
	if errors.Is(err, wire.ErrPeerCNUnavailable) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return r.nodes.NodeIDByCN(cn)
}

// instanceOnNode reports whether the line's instance belongs to its app and
// runs on node, or ran there before it migrated. An unknown instance is
// not the caller's.
func (r *RuntimeLogEventsReceiver) instanceOnNode(ctx context.Context, event state.LogEvent, node string, onNode map[string]bool) (bool, error) {
	key := event.InstanceID + "/" + event.AppID
	if own, seen := onNode[key]; seen {
		return own, nil
	}
	if !isUUID(event.InstanceID) {
		onNode[key] = false
		return false, nil
	}
	lookup := r.store.InstanceByID
	if m, ok := r.store.(migrationInstanceStore); ok {
		lookup = m.MigrationInstanceByID
	}
	inst, err := lookup(ctx, event.InstanceID)
	if errors.Is(err, state.ErrNotFound) {
		onNode[key] = false
		return false, nil
	}
	if err != nil {
		return false, err
	}
	own := inst.AppID == event.AppID &&
		(inst.NodeID == node || inst.MigratedFromNodeID != nil && *inst.MigratedFromNodeID == node)
	onNode[key] = own
	return own, nil
}

func isUUID(s string) bool { return uuid.Validate(s) == nil }

// runtimeLogEvent validates the wire shape vmmd owns, including the ledger
// bounds a retry could never satisfy, so an insert error means the database.
func runtimeLogEvent(ev *apidpb.RuntimeLogEvent) (state.LogEvent, bool) {
	if ev == nil {
		return state.LogEvent{}, false
	}
	if _, err := uuid.Parse(ev.GetAppId()); err != nil {
		return state.LogEvent{}, false
	}
	if deployment := ev.GetDeploymentId(); deployment != "" {
		if _, err := uuid.Parse(deployment); err != nil {
			return state.LogEvent{}, false
		}
	}
	level, stream := ev.GetLevel(), ev.GetStream()
	if (level != "warn" && level != "error") || (stream != "stdout" && stream != "stderr" && stream != "system") {
		return state.LogEvent{}, false
	}
	message := strings.TrimRight(ev.GetMessage(), "\r\n")
	if message == "" || len(message) > api.RuntimeLogEventMessageMaxBytes || !utf8.ValidString(message) ||
		ev.GetSourceEventId() == "" || len(ev.GetSourceEventId()) > runtimeLogIDMaxBytes ||
		len(ev.GetInstanceId()) > runtimeLogIDMaxBytes || ev.GetOccurredAtUnixMs() <= 0 {
		return state.LogEvent{}, false
	}
	return state.LogEvent{
		OccurredAt:    time.UnixMilli(ev.GetOccurredAtUnixMs()).UTC(),
		AppID:         ev.GetAppId(),
		DeploymentID:  ev.GetDeploymentId(),
		InstanceID:    ev.GetInstanceId(),
		Source:        state.LogEventSourceRuntime,
		SourceEventID: ev.GetSourceEventId(),
		Level:         level,
		Stream:        stream,
		Message:       message,
	}, true
}

// redactionFields records which patterns fired, never what they matched.
func redactionFields(names []string) json.RawMessage {
	fields, err := json.Marshal(map[string][]string{"redactions": names})
	if err != nil {
		return nil
	}
	return fields
}
