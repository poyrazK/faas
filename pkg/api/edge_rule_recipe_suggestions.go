package api

// ADR-20261020105000: suggested protections. The edge-rules page and CLI recommend
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
	edgeRuleSuggestScanMinRequests  = 20
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
	if scan := s.scannerSuggestion(); scan != nil {
		out = append(out, *scan)
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
		if (busy || failing) && !s.pathCovered("throttle", r.Path, EdgeRuleRecipeRateLimitPath) {
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
		sg := suggestion(EdgeRuleRecipeRateLimitPath, map[string]string{"path": r.Path, "methods": "POST"},
			reason+". A per-IP limit slows credential stuffing without affecting normal sign-ins.")
		sg.Alternative = s.challengeAlternative(r.Path)
		out = append(out, sg)
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
	if requests < edgeRuleSuggestAdminMinRequests || s.recipeAdded(EdgeRuleRecipeAllowIPsOnly) || s.pathCovered("ip", "/admin", EdgeRuleRecipeAllowIPsOnly) {
		return nil
	}
	sg := suggestion(EdgeRuleRecipeAllowIPsOnly, map[string]string{"path": "/admin/**"},
		fmt.Sprintf("/admin received %d requests in %s and is open to every IP. Limit it to your own addresses.", requests, s.window()))
	sg.Alternative = s.challengeAlternative("/admin/**")
	return &sg
}

func (s EdgeRuleRecipeSuggestionSignals) window() string {
	if s.Days == 1 {
		return "the last day"
	}
	return fmt.Sprintf("the last %d days", max(s.Days, 1))
}

// scannerSuggestion suggests block-scanner-paths when requests probed the
// default scanner paths. A non-secret prefix the app answers successfully
// for most of its requests is real software (a WordPress site's /wp-admin)
// and is left out of the suggested list.
func (s EdgeRuleRecipeSuggestionSignals) scannerSuggestion() *EdgeRuleRecipeSuggestion {
	if s.recipeAdded(EdgeRuleRecipeBlockScannerPaths) {
		return nil
	}
	var probes int64
	probed := map[string]bool{}
	served := map[string]bool{}
	for _, r := range s.Routes {
		for _, sp := range EdgeRuleScannerPaths {
			if !strings.HasPrefix(r.Path, sp.Prefix) {
				continue
			}
			probes += r.Requests
			probed[sp.Prefix] = true
			if !sp.Secret && (r.Requests-r.ErrorRequests)*2 > r.Requests {
				served[sp.Prefix] = true
			}
			break
		}
	}
	if probes < edgeRuleSuggestScanMinRequests {
		return nil
	}
	var keep, seen []string
	for _, sp := range EdgeRuleScannerPaths {
		if !served[sp.Prefix] {
			keep = append(keep, sp.Prefix)
		}
		if probed[sp.Prefix] && len(seen) < 3 {
			seen = append(seen, sp.Prefix)
		}
	}
	var params map[string]string
	if len(served) > 0 {
		params = map[string]string{"paths": strings.Join(keep, ", ")}
	}
	reason := fmt.Sprintf("%d requests in %s probed paths such as %s: vulnerability scans, not your users.", probes, s.window(), strings.Join(seen, ", "))
	if len(served) > 0 {
		reason += " Paths your app answers are left out."
	}
	sg := suggestion(EdgeRuleRecipeBlockScannerPaths, params, reason)
	return &sg
}

// challengeAlternative offers challenge-path on path, unless an enabled
// challenge rule already covers it.
func (s EdgeRuleRecipeSuggestionSignals) challengeAlternative(path string) *EdgeRuleRecipeSuggestionAlternative {
	if s.pathCovered("challenge", strings.TrimSuffix(path, "/**"), EdgeRuleRecipeChallengePath) {
		return nil
	}
	recipe, _ := EdgeRuleRecipeByID(EdgeRuleRecipeChallengePath)
	return &EdgeRuleRecipeSuggestionAlternative{Recipe: recipe.ID, Title: recipe.Title, Params: map[string]string{"path": path}}
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
// A rule another recipe created does not count: its match condition does
// something else (the bot recipe's ip rule covers /** but only bots).
func (s EdgeRuleRecipeSuggestionSignals) pathCovered(kind, path, recipeID string) bool {
	for _, r := range s.Rules {
		if !r.Enabled || r.Kind != kind || r.fromOtherRecipe(recipeID) {
			continue
		}
		if ok, err := MatchEdgeRulePath(r.MatchPath, path); err == nil && ok {
			return true
		}
	}
	return false
}

func (r EdgeRuleRecipeSuggestionRule) fromOtherRecipe(recipeID string) bool {
	for _, recipe := range EdgeRuleRecipes {
		if recipe.ID != recipeID && r.Description == EdgeRuleRecipeDescription(recipe.ID) {
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
