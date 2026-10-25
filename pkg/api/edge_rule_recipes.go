package api

// ADR-986: edge protection recipes. A recipe is a named, parameterised
// template that expands to one ordinary CreateEdgeRuleRequest, so it adds no
// rule kind, no API surface and no gateway behaviour: the dashboard and the
// CLI build the request here and send it through the normal create path,
// which keeps validation, plan gates and quotas in one place. Recipes start
// in log mode unless the caller asks to enforce immediately.

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// EdgeRuleRecipeParam is one input a recipe asks for.
type EdgeRuleRecipeParam struct {
	Name        string
	Label       string
	Help        string
	Default     string
	Placeholder string
	Required    bool
}

// EdgeRuleRecipe describes one recipe. LogNote explains what the log-mode
// match count means for it; Kind is the rule kind it creates, so callers can
// tell which plan gate applies.
type EdgeRuleRecipe struct {
	ID      string
	Title   string
	Summary string
	LogNote string
	Kind    string
	Params  []EdgeRuleRecipeParam
	build   func(v recipeValues) (CreateEdgeRuleRequest, error)
}

// Recipe IDs.
const (
	EdgeRuleRecipeBlockUnverifiedBots = "block-unverified-bots"
	EdgeRuleRecipeRateLimitPath       = "rate-limit-path"
	EdgeRuleRecipeAllowIPsOnly        = "allow-ips-only"
	EdgeRuleRecipeBlockCountries      = "block-countries"
	EdgeRuleRecipeBlockScannerPaths   = "block-scanner-paths"
	EdgeRuleRecipeChallengePath       = "challenge-path"
	EdgeRuleRecipeControlAICrawlers   = "control-ai-crawlers"
)

// EdgeRuleScannerPath is one path prefix vulnerability scanners probe.
// Secret paths (credentials, version-control metadata) are never meant to
// be public; the others belong to software an app could really run (a
// WordPress site serves /wp-admin), so a suggestion leaves those out when
// the app answers them successfully (ADR-20261020105000).
type EdgeRuleScannerPath struct {
	Prefix string
	Secret bool
}

// EdgeRuleScannerPaths is block-scanner-paths' default list (ADR-986).
var EdgeRuleScannerPaths = []EdgeRuleScannerPath{
	{"/.env", true}, {"/.git/", true}, {"/.svn/", true}, {"/.hg/", true},
	{"/.aws/", true}, {"/.ssh/", true}, {"/.DS_Store", true}, {"/vendor/phpunit/", true},
	{"/wp-", false}, {"/xmlrpc.php", false}, {"/phpmyadmin", false}, {"/pma/", false},
	{"/cgi-bin/", false}, {"/server-status", false},
}

// EdgeRuleScannerPrefixes returns the default scanner prefixes.
func EdgeRuleScannerPrefixes() []string {
	out := make([]string, len(EdgeRuleScannerPaths))
	for i, p := range EdgeRuleScannerPaths {
		out[i] = p.Prefix
	}
	return out
}

// Bounds on recipe inputs.
const (
	edgeRuleRecipeMaxPerMinute = 6000
	edgeRuleRecipeMaxBurst     = 1000
)

// recipePenaltyParam boxes a client the recipe's rule keeps refusing
// (ADR-20261022105846); empty leaves the rule without a penalty.
// EdgeRuleRecipePenaltyParamName is the recipe parameter that sets a
// penalty box (ADR-20261022105846).
const EdgeRuleRecipePenaltyParamName = "penalty_minutes"

var recipePenaltyParam = EdgeRuleRecipeParam{Name: EdgeRuleRecipePenaltyParamName, Label: "Penalty box (minutes)",
	Help: "Optional: once the rule has refused a client, block that client from the whole app for this many minutes (1 to 1440)."}

// recipePenaltyAfter is how many refusals box a client, by recipe: a
// scanner path is suspect on the first hit, a rate limit only when the
// client keeps going.
var recipePenaltyAfter = map[string]int{EdgeRuleRecipeBlockScannerPaths: 1, EdgeRuleRecipeRateLimitPath: 3}

var recipePathParam = EdgeRuleRecipeParam{Name: "path", Label: "Path", Help: "Path the rule covers: /** is every path, /shop/** is /shop and everything under it.", Default: "/**"}

// EdgeRuleRecipes is the recipe catalog, in display order.
var EdgeRuleRecipes = []EdgeRuleRecipe{
	{
		ID:      EdgeRuleRecipeBlockUnverifiedBots,
		Kind:    "ip",
		Title:   "Block unverified bots",
		Summary: "Answer 403 to requests whose User-Agent says bot or crawler, unless the crawler is verified (Googlebot, Bingbot, Applebot, …). Real search engines keep crawling.",
		LogNote: "In log mode the match count is the requests it would block.",
		Params:  []EdgeRuleRecipeParam{recipePathParam},
		build:   buildBlockUnverifiedBots,
	},
	{
		ID:      EdgeRuleRecipeRateLimitPath,
		Kind:    "throttle",
		Title:   "Rate-limit a path per client IP",
		Summary: "Limit how often one client IP may call a path, such as a login or sign-up form. Over the limit the edge answers 429.",
		LogNote: "In log mode the match count is every request the limit covers, not only the ones it would reject.",
		Params: []EdgeRuleRecipeParam{
			{Name: "path", Label: "Path", Help: "Path to limit.", Default: "/login"},
			{Name: "methods", Label: "Methods", Help: "Comma-separated; empty means every method.", Default: "POST"},
			{Name: "per_minute", Label: "Requests per minute", Help: "Sustained rate per client IP.", Default: "10"},
			{Name: "burst", Label: "Burst", Help: "Requests allowed at once before the rate applies; empty uses the per-minute value.", Placeholder: "10"},
			recipePenaltyParam,
		},
		build: buildRateLimitPath,
	},
	{
		ID:      EdgeRuleRecipeAllowIPsOnly,
		Kind:    "ip",
		Title:   "Allow only listed IPs on a path",
		Summary: "Answer 403 on a path, such as an admin area, unless the client IP is in your list. A client whose IP cannot be determined is blocked.",
		LogNote: "In log mode the match count is the requests it would block.",
		Params: []EdgeRuleRecipeParam{
			{Name: "path", Label: "Path", Help: "Path to protect: /admin/** is /admin and everything under it.", Default: "/admin/**"},
			{Name: "cidrs", Label: "Allowed IPs", Help: "IPs or CIDR ranges, comma- or space-separated.", Placeholder: "203.0.113.7, 198.51.100.0/24", Required: true},
		},
		build: buildAllowIPsOnly,
	},
	{
		ID:      EdgeRuleRecipeBlockCountries,
		Kind:    "ip",
		Title:   "Block countries",
		Summary: "Answer 403 to clients located in the listed countries. A client whose country is unknown is let through.",
		LogNote: "In log mode the match count is the requests it would block.",
		Params: []EdgeRuleRecipeParam{
			recipePathParam,
			{Name: "countries", Label: "Countries", Help: "ISO 3166-1 alpha-2 codes, comma- or space-separated.", Placeholder: "KP, IR", Required: true},
		},
		build: buildBlockCountries,
	},
	{
		ID:      EdgeRuleRecipeBlockScannerPaths,
		Kind:    "ip",
		Title:   "Block vulnerability scanner paths",
		Summary: "Answer 403 to requests for paths scanners probe on every public site, such as /.env, /.git/ and /wp-login.php. Remove any your app really serves.",
		LogNote: "In log mode the match count is the requests it would block.",
		Params: []EdgeRuleRecipeParam{
			{Name: "paths", Label: "Path prefixes", Help: "Blocked when a request path starts with one of these; comma- or space-separated.", Default: strings.Join(EdgeRuleScannerPrefixes(), ", ")},
			recipePenaltyParam,
		},
		build: buildBlockScannerPaths,
	},
	{
		ID:      EdgeRuleRecipeChallengePath,
		Kind:    "challenge",
		Title:   "Challenge visitors on a path",
		Summary: "Ask browsers on a path, such as a sign-in page or a whole site under attack, to solve a short proof of work before they reach your app. Verified search crawlers and requests with a verified API key or JWT pass straight through; scripts that cannot run JavaScript get 403.",
		LogNote: "In log mode the match count is the requests it would challenge, including visitors who would already hold a pass.",
		Params: []EdgeRuleRecipeParam{
			{Name: "path", Label: "Path", Help: "Path to challenge: /** is the whole site, /login is one page and the form it posts to.", Default: "/**"},
			{Name: "methods", Label: "Methods", Help: "Comma-separated; empty means every method, so a sign-in page and its form post are both covered."},
		},
		build: buildChallengePath,
	},
	{
		ID:      EdgeRuleRecipeControlAICrawlers,
		Kind:    "ip",
		Title:   "Control AI crawlers",
		Summary: "Block or challenge the crawlers AI companies run (GPTBot, ClaudeBot, CCBot, PerplexityBot, Bytespider and others) by what they fetch for: training models, AI search, or assistants answering a user. By default it blocks training crawlers only; search engines and people are unaffected.",
		LogNote: "In log mode the match count is the AI crawler requests it would block or challenge.",
		Params: []EdgeRuleRecipeParam{
			{Name: "purposes", Label: "Purposes", Help: "Comma-separated: training (collects pages to train models), search (indexes pages for AI search), assistant (fetches a page a user asked about), or all.", Default: AICrawlerPurposeTraining},
			{Name: "action", Label: "Action", Help: "block answers 403; challenge asks for the browser check, which crawlers cannot pass.", Default: "block"},
			recipePathParam,
		},
		build: buildControlAICrawlers,
	},
}

// EdgeRuleRecipeByID returns the recipe with id.
func EdgeRuleRecipeByID(id string) (EdgeRuleRecipe, bool) {
	for _, r := range EdgeRuleRecipes {
		if r.ID == id {
			return r, true
		}
	}
	return EdgeRuleRecipe{}, false
}

// BuildEdgeRuleRecipe expands recipe id for host into a create request.
// values holds the parameters by name; a missing or blank one takes the
// recipe's default. The rule is created in log mode unless enforce is set.
// The result still goes through CreateEdgeRuleRequest validation on the
// server.
func BuildEdgeRuleRecipe(id, host string, values map[string]string, enforce bool) (CreateEdgeRuleRequest, *Problem) {
	recipe, ok := EdgeRuleRecipeByID(id)
	if !ok {
		return CreateEdgeRuleRequest{}, ErrValidation(fmt.Sprintf("unknown recipe %q (%s)", id, strings.Join(EdgeRuleRecipeIDs(), ", ")))
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return CreateEdgeRuleRequest{}, ErrValidation("recipe needs a host")
	}
	v := recipeValues{}
	for _, p := range recipe.Params {
		val := strings.TrimSpace(values[p.Name])
		if val == "" {
			val = p.Default
		}
		if val == "" && p.Required {
			return CreateEdgeRuleRequest{}, ErrValidation(fmt.Sprintf("recipe %s needs %s", id, p.Name))
		}
		v[p.Name] = val
	}
	for name := range values {
		if !recipeHasParam(recipe, name) {
			return CreateEdgeRuleRequest{}, ErrValidation(fmt.Sprintf("recipe %s has no parameter %q", id, name))
		}
	}
	req, err := recipe.build(v)
	if err != nil {
		return CreateEdgeRuleRequest{}, ErrValidation(fmt.Sprintf("recipe %s: %v", id, err))
	}
	if raw := v[recipePenaltyParam.Name]; raw != "" {
		minutes, err := strconv.Atoi(raw)
		if err != nil || minutes < 1 || minutes > MaxEdgePenaltySeconds/60 {
			return CreateEdgeRuleRequest{}, ErrValidation(fmt.Sprintf("recipe %s: penalty_minutes must be a whole number from 1 to %d", id, MaxEdgePenaltySeconds/60))
		}
		req.Penalty = &EdgeRulePenalty{Seconds: minutes * 60, After: recipePenaltyAfter[recipe.ID], WithinSeconds: EdgePenaltyDefaultWithinSeconds}
	}
	req.MatchHost = host
	req.Name = recipe.Title
	req.Description = EdgeRuleRecipeDescription(recipe.ID)
	enabled := true
	req.Enabled = &enabled
	req.Mode = EdgeRuleModeLog
	if enforce {
		req.Mode = EdgeRuleModeEnforce
	}
	return req, nil
}

// EdgeRuleRecipeDescription is the description a recipe stamps on its rule,
// which also lets a client tell that the recipe was already added.
func EdgeRuleRecipeDescription(id string) string {
	return "Created from the " + id + " protection recipe."
}

// EdgeRuleRecipeIDs lists the recipe IDs in catalog order.
func EdgeRuleRecipeIDs() []string {
	ids := make([]string, len(EdgeRuleRecipes))
	for i, r := range EdgeRuleRecipes {
		ids[i] = r.ID
	}
	return ids
}

type recipeValues map[string]string

func recipeHasParam(r EdgeRuleRecipe, name string) bool {
	for _, p := range r.Params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// recipeDenyAll is a kind=ip action that denies every client. Blocking
// recipes pair it with a match condition, so the condition alone decides
// what is blocked and a log-mode match count is exactly the requests the
// rule would block. kind=respond is not used: it is preview-only.
func recipeDenyAll() json.RawMessage {
	action, _ := json.Marshal(EdgeRuleIPAction{Deny: []string{"0.0.0.0/0", "::/0"}})
	return action
}

func buildBlockUnverifiedBots(v recipeValues) (CreateEdgeRuleRequest, error) {
	return CreateEdgeRuleRequest{
		MatchPath: v["path"],
		Kind:      "ip",
		Action:    recipeDenyAll(),
		Match: &EdgeRuleMatchExpr{All: []EdgeRuleMatchExpr{
			{Field: "ua_family", Op: "eq", Value: UAFamilyBot},
			{Field: "verified_bot", Op: "missing"},
		}},
	}, nil
}

func buildRateLimitPath(v recipeValues) (CreateEdgeRuleRequest, error) {
	perMinute, err := strconv.Atoi(v["per_minute"])
	if err != nil || perMinute < 1 || perMinute > edgeRuleRecipeMaxPerMinute {
		return CreateEdgeRuleRequest{}, fmt.Errorf("per_minute must be a whole number from 1 to %d", edgeRuleRecipeMaxPerMinute)
	}
	burst := perMinute
	if raw := v["burst"]; raw != "" {
		if burst, err = strconv.Atoi(raw); err != nil || burst < 1 || burst > edgeRuleRecipeMaxBurst {
			return CreateEdgeRuleRequest{}, fmt.Errorf("burst must be a whole number from 1 to %d", edgeRuleRecipeMaxBurst)
		}
	}
	var methods []string
	for _, m := range recipeSplit(v["methods"]) {
		methods = append(methods, strings.ToUpper(m))
	}
	action, _ := json.Marshal(EdgeRuleThrottleAction{RequestsPerSecond: float64(perMinute) / 60, Burst: burst, KeyBy: "ip"})
	return CreateEdgeRuleRequest{MatchPath: v["path"], MatchMethods: methods, Kind: "throttle", Action: action}, nil
}

func buildAllowIPsOnly(v recipeValues) (CreateEdgeRuleRequest, error) {
	entries := recipeSplit(v["cidrs"])
	if len(entries) == 0 {
		return CreateEdgeRuleRequest{}, fmt.Errorf("list at least one IP or CIDR")
	}
	if len(entries) > EdgeRuleMatchMaxValues {
		return CreateEdgeRuleRequest{}, fmt.Errorf("at most %d IPs or CIDRs; use an edge rule list for more", EdgeRuleMatchMaxValues)
	}
	cidrs := make([]string, 0, len(entries))
	for _, e := range entries {
		if ip := net.ParseIP(e); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			e = fmt.Sprintf("%s/%d", ip, bits)
		}
		if _, _, err := net.ParseCIDR(e); err != nil {
			return CreateEdgeRuleRequest{}, fmt.Errorf("%q is not an IP or CIDR", e)
		}
		cidrs = append(cidrs, e)
	}
	return CreateEdgeRuleRequest{
		MatchPath: v["path"],
		Kind:      "ip",
		Action:    recipeDenyAll(),
		Match:     &EdgeRuleMatchExpr{Not: &EdgeRuleMatchExpr{Field: "client_ip", Op: "cidr", Values: cidrs}},
	}, nil
}

func buildBlockCountries(v recipeValues) (CreateEdgeRuleRequest, error) {
	entries := recipeSplit(v["countries"])
	if len(entries) == 0 {
		return CreateEdgeRuleRequest{}, fmt.Errorf("list at least one country code")
	}
	if len(entries) > EdgeRuleMatchMaxValues {
		return CreateEdgeRuleRequest{}, fmt.Errorf("at most %d countries; use an edge rule list for more", EdgeRuleMatchMaxValues)
	}
	codes := make([]string, 0, len(entries))
	for _, e := range entries {
		if err := validateGeoCountryCode(e); err != nil {
			return CreateEdgeRuleRequest{}, err
		}
		codes = append(codes, strings.ToUpper(e))
	}
	return CreateEdgeRuleRequest{
		MatchPath: v["path"],
		Kind:      "ip",
		Action:    recipeDenyAll(),
		Match:     &EdgeRuleMatchExpr{Field: "country", Op: "in", Values: codes},
	}, nil
}

func buildBlockScannerPaths(v recipeValues) (CreateEdgeRuleRequest, error) {
	prefixes := recipeSplit(v["paths"])
	if len(prefixes) == 0 {
		return CreateEdgeRuleRequest{}, fmt.Errorf("list at least one path prefix")
	}
	if len(prefixes) > EdgeRuleMatchMaxValues {
		return CreateEdgeRuleRequest{}, fmt.Errorf("at most %d path prefixes", EdgeRuleMatchMaxValues)
	}
	for _, p := range prefixes {
		if !strings.HasPrefix(p, "/") || len(p) < 2 {
			return CreateEdgeRuleRequest{}, fmt.Errorf("%q is not a path prefix such as /.env", p)
		}
	}
	return CreateEdgeRuleRequest{
		MatchPath: "/**",
		Kind:      "ip",
		Action:    recipeDenyAll(),
		Match:     &EdgeRuleMatchExpr{Field: "path", Op: "prefix", Values: prefixes},
	}, nil
}

func buildChallengePath(v recipeValues) (CreateEdgeRuleRequest, error) {
	var methods []string
	for _, m := range recipeSplit(v["methods"]) {
		methods = append(methods, strings.ToUpper(m))
	}
	action, _ := json.Marshal(EdgeRuleChallengeAction{})
	return CreateEdgeRuleRequest{
		MatchPath:    v["path"],
		MatchMethods: methods,
		Kind:         "challenge",
		Action:       action,
		Match:        &EdgeRuleMatchExpr{Field: "verified_bot", Op: "missing"},
	}, nil
}

func buildControlAICrawlers(v recipeValues) (CreateEdgeRuleRequest, error) {
	var purposes []string
	for _, p := range recipeSplit(strings.ToLower(v["purposes"])) {
		if p == "all" {
			purposes = append([]string(nil), AICrawlerPurposes...)
			break
		}
		if !containsMatchValue(AICrawlerPurposes, p) {
			return CreateEdgeRuleRequest{}, fmt.Errorf("purposes: unknown purpose %q (%s, or all)", p, strings.Join(AICrawlerPurposes, ", "))
		}
		if !containsMatchValue(purposes, p) {
			purposes = append(purposes, p)
		}
	}
	if len(purposes) == 0 {
		return CreateEdgeRuleRequest{}, fmt.Errorf("purposes: name at least one of %s, or all", strings.Join(AICrawlerPurposes, ", "))
	}
	req := CreateEdgeRuleRequest{
		MatchPath: v["path"],
		Match:     &EdgeRuleMatchExpr{Field: "ai_crawler_purpose", Op: "in", Values: purposes},
	}
	switch strings.ToLower(strings.TrimSpace(v["action"])) {
	case "block":
		req.Kind, req.Action = "ip", recipeDenyAll()
	case "challenge":
		req.Kind = "challenge"
		req.Action, _ = json.Marshal(EdgeRuleChallengeAction{})
	default:
		return CreateEdgeRuleRequest{}, fmt.Errorf("action must be block or challenge (got %q)", v["action"])
	}
	return req, nil
}

// recipeSplit splits a comma- or whitespace-separated list.
func recipeSplit(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
}

// Bounds on a recipe's "alert me" threshold (matches per hour).
const (
	EdgeRuleRecipeAlertDefaultPerHour = 100
	edgeRuleRecipeAlertMaxPerHour     = 10_000_000
)

// EdgeRuleRecipeAlert builds the optional "alert me" rule for the edge rule a
// recipe created (ADR-986 amendment): fire when the rule matches more than
// perHour requests in an hour. It counts edge_rule_activity — matches in
// either mode — so the alert keeps working when the rule moves from log mode
// to enforce. Alerts deliver to a webhook only.
func EdgeRuleRecipeAlert(recipeID, edgeRuleID string, perHour int, webhookURL, webhookSecret string) (CreateAlertRuleRequest, *Problem) {
	recipe, ok := EdgeRuleRecipeByID(recipeID)
	if !ok {
		return CreateAlertRuleRequest{}, ErrValidation(fmt.Sprintf("unknown recipe %q", recipeID))
	}
	if edgeRuleID == "" {
		return CreateAlertRuleRequest{}, ErrValidation("recipe alert needs the edge rule id")
	}
	if perHour < 1 || perHour > edgeRuleRecipeAlertMaxPerHour {
		return CreateAlertRuleRequest{}, ErrValidation(fmt.Sprintf("alert threshold must be a whole number of matches per hour from 1 to %d", edgeRuleRecipeAlertMaxPerHour))
	}
	if strings.TrimSpace(webhookURL) == "" || webhookSecret == "" {
		return CreateAlertRuleRequest{}, ErrValidation("recipe alert needs a webhook URL and secret")
	}
	suffix := edgeRuleID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return CreateAlertRuleRequest{
		Name:          fmt.Sprintf("%s spike (%s)", recipe.Title, suffix),
		Metric:        AlertMetricEdgeRuleActivity,
		Comparison:    "gt",
		Threshold:     float64(perHour),
		WindowSpec:    "1h",
		EdgeRuleID:    edgeRuleID,
		WebhookURL:    strings.TrimSpace(webhookURL),
		WebhookSecret: webhookSecret,
	}, nil
}
