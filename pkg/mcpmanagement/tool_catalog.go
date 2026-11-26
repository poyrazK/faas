package mcpmanagement

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/onebox-faas/faas/pkg/safetext"
)

// Some clients refuse a server that lists more than 128 tools (OpenAI's
// function limit), and every listed schema costs context on every turn. When
// the selected catalog is larger than Options.MaxListedTools each session
// lists core tools plus gregale_find_tools; a search adds its matches to that
// session's listing and the SDK sends notifications/tools/list_changed
// (ADR-20261121140000, amended by ADR-20261121190000). Every selected tool
// stays registered with its handler and checks, so a known name can be called
// directly; the cap applies to what tools/list shows.
const (
	findToolsName = "gregale_find_tools"
	// DefaultMaxListedTools is the cap the CLI and hosted bridge apply.
	DefaultMaxListedTools = 128
	maxFoundTools         = 20
	// capabilityRefresh is how often tools/list re-reads the account's
	// capabilities, so a capability released or withdrawn during a long
	// connection adds or removes its tools.
	capabilityRefresh = 5 * time.Minute
)

type queuedTool struct {
	name        string
	description string
	add         func()
}

type toolCatalog struct {
	mu      sync.Mutex
	pending []queuedTool
	// deferring is set when the selected catalog exceeds limit; core counts
	// the tools every session lists, gregale_find_tools included.
	deferring bool
	limit     int
	core      int
	// findable are the registered non-core tools a session lists once found.
	findable map[string]queuedTool
	// found lists each session's found tools, oldest first; the oldest
	// leave its listing when a search would pass the limit.
	found map[*mcp.ServerSession][]string
	// gated are the selected capability-gated tools; hidden are those whose
	// capability is off for the account and are not registered.
	gated        map[string]queuedTool
	hidden       map[string]bool
	capsReadAt   time.Time
	refreshEvery time.Duration
}

func (m *management) queueTool(name, description string, add func()) {
	m.catalog.mu.Lock()
	defer m.catalog.mu.Unlock()
	m.catalog.pending = append(m.catalog.pending, queuedTool{name: name, description: description, add: add})
}

// toolCapability names the product capability (pkg/productcap) behind a
// tool whose API is refused while that capability is off. Previews were
// listed to every account although each call failed (ADR-20261121140000).
var toolCapability = map[string]string{
	"gregale_search_logs":           "log-search",
	"gregale_get_log_facets":        "log-search",
	"gregale_list_incidents":        "incidents",
	"gregale_get_incident":          "incidents",
	"gregale_acknowledge_incident":  "incidents",
	"gregale_add_incident_note":     "incidents",
	"gregale_get_insight_events":    "product-analytics",
	"gregale_get_insight_funnel":    "product-analytics",
	"gregale_get_insight_retention": "product-analytics",
	"gregale_get_insight_requests":  "product-analytics",
	"gregale_list_insight_cohorts":  "product-analytics",
	"gregale_list_saved_insights":   "saved-insights",
	"gregale_run_saved_insight":     "saved-insights",
}

// unavailableCapabilities reads the account's hidden and disabled
// capabilities. ok is false when the read failed; the caller then keeps
// what it showed, and apid still refuses calls to an unavailable feature.
func (m *management) unavailableCapabilities(ctx context.Context) (map[string]bool, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := m.client.GetCapabilities(ctx)
	if err != nil {
		return nil, false
	}
	out := map[string]bool{}
	for _, id := range c.HiddenCapabilities {
		out[id] = true
	}
	for _, capability := range c.Capabilities {
		if !capability.Enabled {
			out[capability.Key] = true
		}
	}
	return out, true
}

// publishCatalog registers the queued tools and, above the cap, lists only
// core per session. Agent-key servers select toolsets per request and list
// them all. A capability read happens only when a gated tool is selected,
// so a core catalog starts without an API request.
//
//nolint:contextcheck // Server construction has no request context; the read is bounded by its own timeout.
func (m *management) publishCatalog(s *mcp.Server) {
	c := &m.catalog
	c.mu.Lock()
	pending := c.pending
	c.pending = nil
	c.gated, c.hidden, c.found, c.refreshEvery = map[string]queuedTool{}, map[string]bool{}, map[*mcp.ServerSession][]string{}, capabilityRefresh
	if m.opts.HideUnavailableCapabilities {
		for _, t := range pending {
			if toolCapability[t.name] != "" {
				c.gated[t.name] = t
			}
		}
	}
	c.mu.Unlock()
	if len(c.gated) > 0 {
		unavailable, _ := m.unavailableCapabilities(context.Background())
		c.mu.Lock()
		c.capsReadAt = time.Now()
		for name := range c.gated {
			if unavailable[toolCapability[name]] {
				c.hidden[name] = true
			}
		}
		c.mu.Unlock()
	}
	available := slices.DeleteFunc(pending, func(t queuedTool) bool { return c.hidden[t.name] })
	limit := m.opts.MaxListedTools
	c.deferring = limit > 0 && len(available) > limit && !m.opts.agentKeyToolsetSelection
	c.limit, c.findable = limit, map[string]queuedTool{}
	for _, t := range available {
		if c.deferring && !toolsetIncludes(t.name, ToolsetCore) {
			c.findable[t.name] = t
		} else {
			c.core++
		}
		t.add()
	}
	if c.deferring {
		c.core++
		m.toolNames[findToolsName] = true
		m.toolWrites[findToolsName] = false
		destructive := false
		mcp.AddTool(s, &mcp.Tool{Name: findToolsName, Title: toolTitle(findToolsName), Description: "Find Gregale tools that are not listed yet and add them to this connection's tool list. This server lists core tools first because the full catalog is larger than many clients accept. Search by task words (for example: postgres schema, cron, domain, rollout, flags, budget); call the returned tools after your client refreshes its tool list.", OutputSchema: registeredToolOutputSchema(findToolsName), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: toolOpenWorld(findToolsName)}},
			toolHandler(m, findToolsName, true, func(ctx context.Context, a findToolsArgs) (any, error) { return m.findTools(ctx, s, a) }))
	}
	if c.deferring || len(c.gated) > 0 {
		s.AddReceivingMiddleware(m.catalogMiddleware(s))
	}
}

// catalogMiddleware refreshes capability-gated tools and narrows each
// session's tools/list page to core plus the tools it found.
func (m *management) catalogMiddleware(s *mcp.Server) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, req)
			}
			m.refreshCapabilities(ctx, s)
			result, err := next(ctx, method, req)
			page, ok := result.(*mcp.ListToolsResult)
			if err != nil || !ok || !m.catalog.deferring {
				return result, err
			}
			session, _ := req.GetSession().(*mcp.ServerSession)
			c := &m.catalog
			c.mu.Lock()
			found := c.found[session]
			filtered := *page
			filtered.Tools = make([]*mcp.Tool, 0, len(page.Tools))
			for _, tool := range page.Tools {
				if _, deferred := c.findable[tool.Name]; !deferred || slices.Contains(found, tool.Name) {
					filtered.Tools = append(filtered.Tools, tool)
				}
			}
			c.mu.Unlock()
			return &filtered, nil
		}
	}
}

// refreshCapabilities re-reads capabilities once per refresh interval and
// registers or removes gated tools whose capability changed. Registering or
// removing a tool sends notifications/tools/list_changed.
func (m *management) refreshCapabilities(ctx context.Context, s *mcp.Server) {
	c := &m.catalog
	c.mu.Lock()
	due := len(c.gated) > 0 && time.Since(c.capsReadAt) >= c.refreshEvery
	if due {
		c.capsReadAt = time.Now()
	}
	c.mu.Unlock()
	if !due {
		return
	}
	unavailable, ok := m.unavailableCapabilities(ctx)
	if !ok {
		return
	}
	var shown []queuedTool
	var removed []string
	c.mu.Lock()
	for name, t := range c.gated {
		off := unavailable[toolCapability[name]]
		switch {
		case off && !c.hidden[name]:
			c.hidden[name] = true
			removed = append(removed, name)
			delete(c.findable, name)
			for session, found := range c.found {
				c.found[session] = slices.DeleteFunc(found, func(n string) bool { return n == name })
			}
		case !off && c.hidden[name]:
			delete(c.hidden, name)
			shown = append(shown, t)
			if c.deferring && !toolsetIncludes(name, ToolsetCore) {
				c.findable[name] = t
			}
		}
	}
	c.mu.Unlock()
	if len(removed) > 0 {
		s.RemoveTools(removed...)
	}
	// Gated names stay in m.toolNames from registration, so telemetry keeps
	// labeling them without a write here.
	for _, t := range shown {
		t.add()
	}
}

type findToolsArgs struct {
	Query string `json:"query" jsonschema:"Words describing the task or part of a tool name"`
	Limit int    `json:"limit,omitempty" jsonschema:"Most tools to add; default 8, at most 20"`
}

type foundTool struct {
	Name        string `json:"name"`
	Toolset     string `json:"toolset,omitempty"`
	Description string `json:"description"`
}

type findToolsResult struct {
	Tools     []foundTool `json:"tools"`
	Removed   []string    `json:"removed,omitempty"`
	Remaining int         `json:"remaining_unlisted"`
	Note      string      `json:"note"`
}

func (m *management) findTools(ctx context.Context, s *mcp.Server, a findToolsArgs) (any, error) {
	words := strings.Fields(strings.ToLower(a.Query))
	if len(words) == 0 || len(a.Query) > 200 {
		return nil, invalid("invalid_query", "query must name the task in at most 200 characters")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 8
	}
	var session *mcp.ServerSession
	if req, ok := ctx.Value(toolRequestKey{}).(*mcp.CallToolRequest); ok {
		session = req.Session
	}
	c := &m.catalog
	c.mu.Lock()
	m.pruneClosedSessionsLocked(s)
	found := c.found[session]
	limit = max(min(limit, maxFoundTools, c.limit-c.core), 0)
	candidates := map[string]queuedTool{}
	for name, t := range c.findable {
		if !slices.Contains(found, name) {
			candidates[name] = t
		}
	}
	matches := matchTools(candidates, words, limit)
	var removed []string
	for len(found) > 0 && c.core+len(found)+len(matches) > c.limit {
		removed = append(removed, found[0])
		found = found[1:]
	}
	for _, t := range matches {
		found = append(found, t.name)
	}
	c.found[session] = found
	remaining := len(c.findable) - len(found)
	c.mu.Unlock()
	out := findToolsResult{Tools: []foundTool{}, Removed: removed, Remaining: remaining}
	for _, t := range matches {
		// Re-adding a registered tool replaces it unchanged and sends
		// notifications/tools/list_changed, so this session lists again.
		t.add()
		set, _ := ToolsetOf(t.name)
		out.Tools = append(out.Tools, foundTool{Name: t.name, Toolset: set, Description: firstSentence(t.description)})
	}
	if len(out.Tools) == 0 {
		out.Note = "No unlisted tool matches; listed tools already cover it, or try other words."
	} else {
		out.Note = "Added to this session's tool list (notifications/tools/list_changed). If your client does not refresh, call the tools by name or reconnect with --toolsets " + toolsetsOf(out.Tools) + "."
	}
	return out, nil
}

// pruneClosedSessionsLocked forgets the found tools of closed sessions, so a
// long-lived hosted server does not accumulate them.
func (m *management) pruneClosedSessionsLocked(s *mcp.Server) {
	live := map[*mcp.ServerSession]bool{}
	for session := range s.Sessions() {
		live[session] = true
	}
	for session := range m.catalog.found {
		if !live[session] {
			delete(m.catalog.found, session)
		}
	}
}

// matchTools ranks unlisted tools: an exact name first, then name words, then
// description words.
func matchTools(candidates map[string]queuedTool, words []string, limit int) []queuedTool {
	type scored struct {
		t     queuedTool
		score int
	}
	var ranked []scored
	for name, t := range candidates {
		score := 0
		description := strings.ToLower(t.description)
		for _, w := range words {
			switch {
			case w == name:
				score += 100
			case strings.Contains(name, w):
				score += 5
			case strings.Contains(description, w):
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{t, score})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].t.name < ranked[j].t.name
	})
	out := []queuedTool{}
	for _, r := range ranked {
		if len(out) == limit {
			break
		}
		out = append(out, r.t)
	}
	return out
}

func firstSentence(description string) string {
	if i := strings.Index(description, ". "); i > 0 {
		description = description[:i+1]
	}
	if len(description) > 240 {
		description = safetext.Truncate(description, 237) + "..."
	}
	return description
}

func toolsetsOf(tools []foundTool) string {
	seen := map[string]bool{ToolsetCore: true}
	sets := []string{ToolsetCore}
	for _, t := range tools {
		if t.Toolset != "" && !seen[t.Toolset] {
			seen[t.Toolset] = true
			sets = append(sets, t.Toolset)
		}
	}
	return strings.Join(sets, ",")
}
