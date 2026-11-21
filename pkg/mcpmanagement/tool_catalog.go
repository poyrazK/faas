package mcpmanagement

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Some clients refuse a server that lists more than 128 tools (OpenAI's
// function limit), and every listed schema costs context on every turn. When
// the selected catalog is larger than Options.MaxListedTools the server lists
// core tools plus gregale_find_tools; a search adds its matches to the
// listing and the SDK sends notifications/tools/list_changed
// (ADR-20261121140000). The other tools keep their handlers and checks; they
// are only absent from tools/list until found.
const (
	findToolsName = "gregale_find_tools"
	// DefaultMaxListedTools is the cap the CLI and hosted bridge apply.
	DefaultMaxListedTools = 128
	maxFoundTools         = 20
)

type queuedTool struct {
	name        string
	description string
	add         func()
}

type toolCatalog struct {
	mu        sync.Mutex
	pending   []queuedTool
	deferring bool
	limit     int
	core      int
	deferred  map[string]queuedTool
	// enabled lists found tools, oldest first; the oldest leave the listing
	// when a search would exceed the limit.
	enabled []queuedTool
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
}

// unavailableCapabilities reads the account's hidden and disabled
// capabilities. A failed read hides nothing: apid still refuses the calls.
func (m *management) unavailableCapabilities() map[string]bool {
	if !m.opts.HideUnavailableCapabilities {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := m.client.GetCapabilities(ctx)
	if err != nil {
		return nil
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
	return out
}

// publishCatalog adds the queued tools, deferring all but core above the cap.
// Agent-key servers select toolsets per request and list them all.
func (m *management) publishCatalog(s *mcp.Server) {
	unavailable := m.unavailableCapabilities()
	c := &m.catalog
	c.mu.Lock()
	pending := slices.DeleteFunc(c.pending, func(t queuedTool) bool {
		if unavailable[toolCapability[t.name]] {
			delete(m.toolNames, t.name)
			return true
		}
		return false
	})
	c.pending = nil
	limit := m.opts.MaxListedTools
	if limit <= 0 || len(pending) <= limit || m.opts.agentKeyToolsetSelection {
		c.mu.Unlock()
		for _, t := range pending {
			t.add()
		}
		return
	}
	c.deferring, c.limit, c.deferred = true, limit, map[string]queuedTool{}
	var listed []queuedTool
	for _, t := range pending {
		if toolsetIncludes(t.name, ToolsetCore) {
			listed = append(listed, t)
		} else {
			c.deferred[t.name] = t
		}
	}
	c.core = len(listed) + 1
	c.mu.Unlock()
	for _, t := range listed {
		t.add()
	}
	m.toolNames[findToolsName] = true
	m.toolWrites[findToolsName] = false
	destructive := false
	mcp.AddTool(s, &mcp.Tool{Name: findToolsName, Description: "Find Gregale tools that are not listed yet and add them to this connection's tool list. This server lists core tools first because the full catalog is larger than many clients accept. Search by task words (for example: postgres schema, cron, domain, rollout, flags, budget); call the returned tools after your client refreshes its tool list.", OutputSchema: registeredToolOutputSchema(findToolsName), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &destructive, IdempotentHint: true}},
		toolHandler(m, findToolsName, true, func(ctx context.Context, a findToolsArgs) (any, error) { return m.findTools(s, a) }))
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

func (m *management) findTools(s *mcp.Server, a findToolsArgs) (any, error) {
	words := strings.Fields(strings.ToLower(a.Query))
	if len(words) == 0 || len(a.Query) > 200 {
		return nil, invalid("invalid_query", "query must name the task in at most 200 characters")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 8
	}
	limit = min(limit, maxFoundTools)
	c := &m.catalog
	c.mu.Lock()
	limit = min(limit, c.limit-c.core)
	matches := matchTools(c.deferred, words, max(limit, 0))
	var removed []queuedTool
	for len(c.enabled) > 0 && c.core+len(c.enabled)+len(matches) > c.limit {
		removed = append(removed, c.enabled[0])
		c.deferred[c.enabled[0].name] = c.enabled[0]
		c.enabled = c.enabled[1:]
	}
	for _, t := range matches {
		delete(c.deferred, t.name)
		c.enabled = append(c.enabled, t)
	}
	remaining := len(c.deferred)
	c.mu.Unlock()
	out := findToolsResult{Tools: []foundTool{}, Remaining: remaining}
	for _, t := range removed {
		out.Removed = append(out.Removed, t.name)
	}
	if len(out.Removed) > 0 {
		s.RemoveTools(out.Removed...)
	}
	for _, t := range matches {
		t.add()
		set, _ := ToolsetOf(t.name)
		out.Tools = append(out.Tools, foundTool{Name: t.name, Toolset: set, Description: firstSentence(t.description)})
	}
	switch {
	case len(out.Tools) == 0:
		out.Note = "No unlisted tool matches; listed tools already cover it, or try other words."
	default:
		out.Note = "Added to this connection's tool list (notifications/tools/list_changed). If your client does not refresh, reconnect with --toolsets " + toolsetsOf(out.Tools) + "."
	}
	return out, nil
}

// matchTools ranks unlisted tools: an exact name first, then name words, then
// description words. Listed tools are not returned again.
func matchTools(deferred map[string]queuedTool, words []string, limit int) []queuedTool {
	type scored struct {
		t     queuedTool
		score int
	}
	var ranked []scored
	for name, t := range deferred {
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
		description = description[:237] + "..."
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
