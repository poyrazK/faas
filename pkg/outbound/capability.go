package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
)

// The agent capability gateway (ADR-20261027090016, amendment 1): outboundd
// serves MCP at CapabilityMCPPath on an agent run's relay. The run's version
// policy decides which tools are listed; every call is counted against the
// run's tool-call budget before it executes; a refused call is a decision
// row. Tools execute here, outside the VM, so no credential enters the guest.

// CapabilityMCPPath is the gateway's MCP endpoint on the relay.
const CapabilityMCPPath = api.AgentCapabilityMCPPath

// Capability decisions, as agent_run_capability_decisions records them and
// outbound_capability_calls_total labels them.
const (
	CapabilityAllowed  = "allowed"
	CapabilityDenied   = "denied"
	CapabilityBudget   = "budget"
	CapabilityDisabled = "disabled"
	CapabilityFailed   = "failed"
)

// capabilitySourceBuiltin labels the built-in tools (ADR-20261027090016 §1).
const capabilitySourceBuiltin = "builtin"

// CapabilityTool is one tool the gateway can execute.
type CapabilityTool interface {
	// Name is the tool's MCP name, one of api.AgentCapabilityTools.
	Name() string
	Description() string
	// InputSchema is the tool's JSON Schema for its arguments.
	InputSchema() map[string]any
	// Enabled reports whether the operator switched the tool on. A tool
	// that is off is never listed.
	Enabled() bool
	// Call runs the tool. A failure the agent should see is a result with
	// isError; err is reserved for the gateway's own faults.
	Call(ctx context.Context, run AgentRunIdentity, args json.RawMessage) (text string, isError bool, err error)
}

// AgentRunCapabilities is what a running agent run may do over the gateway.
type AgentRunCapabilities struct {
	Policy *api.AgentCapabilityPolicy
	Plan   api.Plan
}

// AgentCapabilityStore authorizes a run for the gateway and keeps its
// capability ledger. Implementations must fail closed on store errors.
type AgentCapabilityStore interface {
	// AgentRunCapabilities returns the run's policy and plan when the run is
	// running under the identity's lease and has not been cancelled.
	AgentRunCapabilities(ctx context.Context, run AgentRunIdentity) (AgentRunCapabilities, bool, error)
	// ReserveToolCall counts one call against the run's budget, unless the
	// run has already made budget calls.
	ReserveToolCall(ctx context.Context, run AgentRunIdentity, budget int) (bool, error)
	// RecordDecision records a refused call: denied, budget or disabled.
	RecordDecision(ctx context.Context, run AgentRunIdentity, tool, decision string) error
}

// CapabilityMetrics are the gateway's §12 metrics.
type CapabilityMetrics struct {
	calls     *prometheus.CounterVec
	seconds   *prometheus.HistogramVec
	exhausted *prometheus.CounterVec
	approvals *prometheus.CounterVec
}

// NewCapabilityMetrics registers the gateway's metrics on reg.
func NewCapabilityMetrics(reg prometheus.Registerer) (*CapabilityMetrics, error) {
	m := &CapabilityMetrics{
		calls: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "outbound_capability_calls_total",
			Help: "Capability gateway tool calls by source and decision (ADR-20261027090016)."}, []string{"source", "decision"}),
		seconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "outbound_capability_call_seconds",
			Help:    "Capability gateway tool call latency by source, including the budget reservation.",
			Buckets: []float64{0.005, 0.01, 0.02, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30}}, []string{"source"}),
		exhausted: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_budget_exhausted_total",
			Help: "Agent run capability calls refused by an exhausted budget, by budget kind."}, []string{"kind"}),
		approvals: newApprovalCounter(),
	}
	for _, c := range []prometheus.Collector{m.calls, m.seconds, m.exhausted, m.approvals} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register capability metrics: %w", err)
		}
	}
	return m, nil
}

func (m *CapabilityMetrics) observe(decision string, began time.Time) {
	if m == nil {
		return
	}
	m.calls.WithLabelValues(capabilitySourceBuiltin, decision).Inc()
	if decision == CapabilityAllowed || decision == CapabilityFailed {
		m.seconds.WithLabelValues(capabilitySourceBuiltin).Observe(time.Since(began).Seconds())
	}
	if decision == CapabilityBudget {
		m.exhausted.WithLabelValues("tool_calls").Inc()
	}
}

// CapabilityGateway serves CapabilityMCPPath for agent runs.
type CapabilityGateway struct {
	Store   AgentCapabilityStore
	Tools   []CapabilityTool
	Metrics *CapabilityMetrics
	// ToolTimeout bounds one tool call; zero means a minute.
	ToolTimeout time.Duration
	// ApprovalPoll is how often a held call reads its decision; zero means
	// AgentRunApprovalPollMillis.
	ApprovalPoll time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mcp http.Handler
}

// NewCapabilityGateway returns the gateway. The relay authenticates every
// connection by vmmd's assertion, and no browser can reach it, so the SDK's
// localhost Host check (a DNS-rebinding defense) does not apply: requests
// carry the guest's loopback Host to a listener that is not loopback.
func NewCapabilityGateway(store AgentCapabilityStore, tools []CapabilityTool, metrics *CapabilityMetrics) (*CapabilityGateway, error) {
	if store == nil {
		return nil, errors.New("capability gateway: a store is required")
	}
	g := &CapabilityGateway{Store: store, Tools: tools, Metrics: metrics}
	g.mcp = mcp.NewStreamableHTTPHandler(g.server, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true,
		PropagateRequestCancellation: true, DisableLocalhostProtection: true})
	return g, nil
}

type capabilityRunKey struct{}

type capabilityRun struct {
	run  AgentRunIdentity
	caps AgentRunCapabilities
}

// ServeHTTP answers only agent-run relay requests: every other caller gets
// 404, as if the path did not exist. Each request re-checks the run's lease,
// so a finished or cancelled run loses its tools at once.
func (g *CapabilityGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	run, ok := relayAgentRunFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusNotFound, "outbound_integration_not_found", "Outbound integration not found", "")
		return
	}
	caps, allowed, err := g.Store.AgentRunCapabilities(r.Context(), run)
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_agent_run_authorization_unavailable", "Agent run outbound authorization is unavailable", "1")
		return
	}
	if !allowed {
		writeProblem(w, http.StatusForbidden, "outbound_agent_run_not_authorized", "This agent run is not running under its lease", "")
		return
	}
	if holdsApprovals(caps.Policy) {
		// A held call outlives the relay's write timeout (amendment 2). A
		// writer that cannot move its deadline keeps the server's.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(g.approvalWait()))
	}
	ctx := context.WithValue(r.Context(), capabilityRunKey{}, capabilityRun{run: run, caps: caps})
	g.mcp.ServeHTTP(w, r.WithContext(ctx))
}

// listed are the tools the run may call: switched on, and allowed or
// approve-mode by policy.
func (g *CapabilityGateway) listed(caps AgentRunCapabilities) []CapabilityTool {
	var out []CapabilityTool
	for _, tool := range g.Tools {
		if tool.Enabled() && caps.Policy.Lists(tool.Name()) {
			out = append(out, tool)
		}
	}
	return out
}

// server builds the run's MCP server: only its listed tools, and middleware
// that records a call to any other known tool as a decision.
func (g *CapabilityGateway) server(r *http.Request) *mcp.Server {
	cr, _ := r.Context().Value(capabilityRunKey{}).(capabilityRun)
	s := mcp.NewServer(&mcp.Implementation{Name: "gregale-capabilities", Version: "1"}, nil)
	listed := g.listed(cr.caps)
	for _, tool := range listed {
		description := tool.Description()
		if cr.caps.Policy.Mode(tool.Name()) == api.AgentCapabilityApprove {
			description += fmt.Sprintf(" Each call waits for the seller's approval, for at most %d minutes; a denied or expired call returns an error.",
				api.AgentRunApprovalTimeoutSec/60)
		}
		s.AddTool(&mcp.Tool{Name: tool.Name(), Description: description, InputSchema: tool.InputSchema()},
			g.handler(cr, tool))
	}
	s.AddReceivingMiddleware(g.refuseUnlisted(cr, listed))
	return s
}

// refuseUnlisted answers a call to a tool the run may not use with an error
// result naming why, and records it when it names a tool the gateway has.
func (g *CapabilityGateway) refuseUnlisted(cr capabilityRun, listed []CapabilityTool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if method != "tools/call" || !ok || call.Params == nil {
				return next(ctx, method, req)
			}
			name := call.Params.Name
			if slices.ContainsFunc(listed, func(t CapabilityTool) bool { return t.Name() == name }) {
				return next(ctx, method, req)
			}
			decision, why := CapabilityDenied, fmt.Sprintf("%s is not allowed by this agent version's capability policy", name)
			if g.known(name) && !g.enabled(name) {
				decision, why = CapabilityDisabled, fmt.Sprintf("%s is switched off on this Gregale region", name)
			}
			g.Metrics.observe(decision, time.Now())
			if g.known(name) {
				g.record(ctx, cr.run, name, decision)
			}
			return errorResult(why), nil
		}
	}
}

func (g *CapabilityGateway) known(name string) bool {
	return slices.ContainsFunc(g.Tools, func(t CapabilityTool) bool { return t.Name() == name })
}

func (g *CapabilityGateway) enabled(name string) bool {
	return slices.ContainsFunc(g.Tools, func(t CapabilityTool) bool { return t.Name() == name && t.Enabled() })
}

// handler holds an approve-mode call for its decision, counts the call
// against the run's budget, then runs the tool. A denied or expired call is
// not counted.
func (g *CapabilityGateway) handler(cr capabilityRun, tool CapabilityTool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if cr.caps.Policy.Mode(tool.Name()) == api.AgentCapabilityApprove {
			refused, err := g.awaitApproval(ctx, cr, tool.Name(), req.Params.Arguments)
			if err != nil || refused != nil {
				return refused, err
			}
		}
		began := time.Now()
		budget := cr.caps.Policy.ToolCallBudget(cr.caps.Plan)
		ok, err := g.Store.ReserveToolCall(ctx, cr.run, budget)
		if err != nil {
			g.Metrics.observe(CapabilityFailed, began)
			return nil, fmt.Errorf("capability gateway: budget unavailable: %w", err)
		}
		if !ok {
			g.Metrics.observe(CapabilityBudget, began)
			g.record(ctx, cr.run, tool.Name(), CapabilityBudget)
			return errorResult(fmt.Sprintf("this run has used its %d tool calls (AgentToolCallsPerRunMax)", budget)), nil
		}
		ctx, cancel := context.WithTimeout(ctx, g.toolTimeout())
		defer cancel()
		text, isError, err := tool.Call(ctx, cr.run, req.Params.Arguments)
		if err != nil {
			g.Metrics.observe(CapabilityFailed, began)
			return errorResult(tool.Name() + " is unavailable; try again later"), nil
		}
		g.Metrics.observe(CapabilityAllowed, began)
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isError}
		return res, nil
	}
}

func (g *CapabilityGateway) toolTimeout() time.Duration {
	if g.ToolTimeout <= 0 {
		return time.Minute
	}
	return g.ToolTimeout
}

// record writes a decision row. A store error does not change the answer:
// the call was refused either way.
func (g *CapabilityGateway) record(ctx context.Context, run AgentRunIdentity, tool, decision string) {
	_ = g.Store.RecordDecision(context.WithoutCancel(ctx), run, tool, decision)
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: true}
}
