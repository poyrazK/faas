package api

// ADR-1193: suggested protections. The edge-rules page and CLI recommend
// protection recipes (ADR-986) from the app's own request telemetry: bot
// traffic, busy or failing sign-in paths, and an exposed admin area. This
// file is the pure part — signals in, ranked suggestions out — so the
// thresholds are tested without a database.

import (
	"fmt"
	"sort"
	"strings"
)

// edgeRuleSuggestMaxDays is the most request telemetry suggestions read.
const edgeRuleSuggestMaxDays = 7

// EdgeRuleRecipeSuggestionDays is how many days of request telemetry
// suggestions read on plan: a week, or the plan's telemetry retention when
// shorter. Zero means the plan records no request telemetry (Free).
func EdgeRuleRecipeSuggestionDays(plan Plan) int {
	if !plan.DebugTelemetryEnabled() {
		return 0
	}
	return min(edgeRuleSuggestMaxDays, plan.DebugTelemetryRetentionDays())
}

// Thresholds over the suggestion window. A suggestion needs both a volume
// floor and, where it applies, a share, so a quiet app is not nagged.
const (
	edgeRuleSuggestBotMinRequests   = 500
	edgeRuleSuggestBotMinSharePct   = 5
	edgeRuleSuggestAuthMinRequests  = 300
	edgeRuleSuggestAuthMinErrors    = 50
	edgeRuleSuggestAuthMinErrorPct  = 20
	edgeRuleSuggestAdminMinRequests = 20
	edgeRuleSuggestMaxAuthRoutes    = 2
	edgeRuleSuggestMax              = 5
)

// EdgeRuleRecipeSuggestionRoute is one route's traffic over the window. Path is
// the observed path or template ("/users/{id}"); Method may be empty.
type EdgeRuleRecipeSuggestionRoute struct {
	Method        string
	Path          string
	Requests      int64
	ErrorRequests int64
}

// EdgeRuleRecipeSuggestionRule is the part of an existing rule suggestions read
// to avoid recommending what the app already has.
type EdgeRuleRecipeSuggestionRule struct {
	Kind        string
	MatchPath   string
	Description string
	Enabled     bool
}

// EdgeRuleRecipeSuggestionSignals is everything SuggestEdgeRuleRecipes reads.
// Days is the window the counts cover, quoted in each reason.
type EdgeRuleRecipeSuggestionSignals struct {
	Days          int
	TotalRequests int64
	BotRequests   int64
	Routes        []EdgeRuleRecipeSuggestionRoute
	Rules         []EdgeRuleRecipeSuggestionRule
}

// authPathSegments mark a sign-in, sign-up or credential path.
var authPathSegments = map[string]bool{
	"login": true, "signin": true, "sign-in": true, "sign_in": true, "logon": true,
	"auth": true, "authenticate": true, "session": true, "sessions": true, "token": true,
	"register": true, "signup": true, "sign-up": true, "sign_up": true,
	"password": true, "reset-password": true, "forgot-password": true,
	"otp": true, "verify": true, "2fa": true, "mfa": true,
}

// SuggestEdgeRuleRecipes ranks recipe suggestions for an app's traffic.
// Recipes already added, and paths an enabled rule of the same kind already
// covers, are skipped.
func SuggestEdgeRuleRecipes(s EdgeRuleRecipeSuggestionSignals) []EdgeRuleRecipeSuggestion {
	var out []EdgeRuleRecipeSuggestion
	if s.BotRequests >= edgeRuleSuggestBotMinRequests && s.TotalRequests > 0 &&
		s.BotRequests*100 >= s.TotalRequests*edgeRuleSuggestBotMinSharePct && !s.recipeAdded(EdgeRuleRecipeBlockUnverifiedBots) {
		out = append(out, suggestion(EdgeRuleRecipeBlockUnverifiedBots, nil,
			fmt.Sprintf("%d requests (%d%%) in %s came from clients that call themselves bots. Verified crawlers such as Googlebot stay allowed.",
				s.BotRequests, s.BotRequests*100/s.TotalRequests, s.window())))
	}
	out = append(out, s.authSuggestions()...)
	if admin := s.adminSuggestion(); admin != nil {
		out = append(out, *admin)
	}
	if len(out) > edgeRuleSuggestMax {
		out = out[:edgeRuleSuggestMax]
	}
	return out
}

func (s EdgeRuleRecipeSuggestionSignals) authSuggestions() []EdgeRuleRecipeSuggestion {
	candidates := []EdgeRuleRecipeSuggestionRoute{}
	for _, r := range s.Routes {
		if !isAuthPath(r.Path) || !strings.EqualFold(r.Method, "POST") || strings.Contains(r.Path, "{") {
			continue
		}
		busy := r.Requests >= edgeRuleSuggestAuthMinRequests
		failing := r.ErrorRequests >= edgeRuleSuggestAuthMinErrors && r.ErrorRequests*100 >= r.Requests*edgeRuleSuggestAuthMinErrorPct
		if (busy || failing) && !s.pathCovered("throttle", r.Path) {
			candidates = append(candidates, r)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Requests > candidates[j].Requests })
	var out []EdgeRuleRecipeSuggestion
	for i, r := range candidates {
		if i == edgeRuleSuggestMaxAuthRoutes {
			break
		}
		reason := fmt.Sprintf("POST %s received %d requests in %s", r.Path, r.Requests, s.window())
		if r.ErrorRequests > 0 {
			reason += fmt.Sprintf(", %d%% of them failed", r.ErrorRequests*100/max(r.Requests, 1))
		}
		out = append(out, suggestion(EdgeRuleRecipeRateLimitPath, map[string]string{"path": r.Path, "methods": "POST"},
			reason+". A per-IP limit slows credential stuffing without affecting normal sign-ins."))
	}
	return out
}

func (s EdgeRuleRecipeSuggestionSignals) adminSuggestion() *EdgeRuleRecipeSuggestion {
	var requests int64
	for _, r := range s.Routes {
		if p := strings.ToLower(r.Path); p == "/admin" || strings.HasPrefix(p, "/admin/") {
			requests += r.Requests
		}
	}
	if requests < edgeRuleSuggestAdminMinRequests || s.recipeAdded(EdgeRuleRecipeAllowIPsOnly) || s.pathCovered("ip", "/admin") {
		return nil
	}
	sg := suggestion(EdgeRuleRecipeAllowIPsOnly, map[string]string{"path": "/admin/**"},
		fmt.Sprintf("/admin received %d requests in %s and is open to every IP. Limit it to your own addresses.", requests, s.window()))
	return &sg
}

func (s EdgeRuleRecipeSuggestionSignals) window() string {
	if s.Days == 1 {
		return "the last day"
	}
	return fmt.Sprintf("the last %d days", max(s.Days, 1))
}

func (s EdgeRuleRecipeSuggestionSignals) recipeAdded(id string) bool {
	description := EdgeRuleRecipeDescription(id)
	for _, r := range s.Rules {
		if r.Description == description {
			return true
		}
	}
	return false
}

// pathCovered reports whether an enabled rule of kind already matches path.
func (s EdgeRuleRecipeSuggestionSignals) pathCovered(kind, path string) bool {
	for _, r := range s.Rules {
		if !r.Enabled || r.Kind != kind {
			continue
		}
		if ok, err := MatchEdgeRulePath(r.MatchPath, path); err == nil && ok {
			return true
		}
	}
	return false
}

func isAuthPath(path string) bool {
	for _, seg := range strings.Split(strings.ToLower(path), "/") {
		if authPathSegments[seg] {
			return true
		}
	}
	return false
}

func suggestion(recipeID string, params map[string]string, reason string) EdgeRuleRecipeSuggestion {
	recipe, _ := EdgeRuleRecipeByID(recipeID)
	return EdgeRuleRecipeSuggestion{Recipe: recipe.ID, Title: recipe.Title, Reason: reason, Params: params}
}

// SplitEdgeRuleRecipeSuggestionRoute splits a request-telemetry route label
// ("POST /login") into method and path; a label without a method is a path.
func SplitEdgeRuleRecipeSuggestionRoute(label string) (method, path string) {
	if m, p, ok := strings.Cut(label, " "); ok && strings.HasPrefix(p, "/") {
		return m, p
	}
	return "", label
}
