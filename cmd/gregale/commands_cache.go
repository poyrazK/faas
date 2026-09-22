package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const cacheDeclareUsage = "usage: gregale cache GET|HEAD <path> for <duration> [--app SLUG] [--host HOST] [--stale-while-revalidate DURATION] [--stale-if-error DURATION] [--vary-on HEADER] [--priority N]"

// cmdCache implements the declarative cache shorthand and explicit purges.
// The shorthand resolves --app from linked project context when omitted, so
// the common case reads exactly like the product contract:
//
//	gregale cache GET /products/:id for 30s
func cmdCache(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, cacheDeclareUsage+"\n       gregale cache purge <slug> [--path GLOB]", "cache")
		return 1
	}
	switch strings.ToUpper(args[0]) {
	case "GET", "HEAD":
		return cmdCacheDeclare(args)
	case "PURGE":
		return cmdCachePurge(args[1:])
	default:
		PrintUsage(os.Stderr, cacheDeclareUsage+"\n       gregale cache purge <slug> [--path GLOB]", "cache")
		return 1
	}
}

func cmdCachePurge(args []string) int {
	fs := newFlagSet("cache purge", flag.ContinueOnError)
	pathGlob := fs.String("path", "", "optional normalized request path glob")
	// Accept both the documented positional-first form and the
	// conventional flags-first spelling. The standard flag package
	// otherwise stops parsing as soon as it sees the app slug.
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) {
		PrintUsage(os.Stderr, "usage: gregale cache purge <slug> [--path GLOB]", "cache")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	slug := positional[0]
	if err := client.PurgeAppCache(context.Background(), slug, *pathGlob); err != nil {
		return printErr("Cache purge failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"purged": true,
			"app":    slug,
			"path":   *pathGlob,
		}))
	}
	if *pathGlob == "" {
		_, _ = fmt.Fprintf(osStdout, "Purged response cache for %s\n", slug)
	} else {
		_, _ = fmt.Fprintf(osStdout, "Purged response cache for %s (%s)\n", slug, *pathGlob)
	}
	return 0
}

func cmdCacheDeclare(args []string) int {
	fs := newFlagSet("cache", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug (defaults to linked project context)")
	hostFlag := fs.String("host", "", "hostname to match (defaults to the app canonical URL)")
	staleWhileRevalidate := fs.String("stale-while-revalidate", "0s", "serve stale while refreshing (max 5m)")
	staleIfError := fs.String("stale-if-error", "5m", "serve stale when the origin fails (max 5m; 0s disables)")
	priority := fs.Int("priority", 100, "match priority (lower wins)")
	var varyOn multiFlag
	fs.Var(&varyOn, "vary-on", "header included in the cache key (Accept-Language|Accept-Encoding; repeat)")

	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 4 || !strings.EqualFold(positional[2], "for") {
		PrintUsage(os.Stderr, cacheDeclareUsage, "cache")
		return 1
	}
	method := strings.ToUpper(positional[0])
	if method != httpMethodGet && method != httpMethodHead {
		return printErr("Invalid cache method", fmt.Errorf("must be GET or HEAD; got %q", positional[0]))
	}
	matchPath, err := cacheRoutePattern(positional[1])
	if err != nil {
		return printErr("Invalid cache path", err)
	}
	maxAge, err := cacheDurationSeconds("cache duration", positional[3], api.ResponseCacheMaxAgeMaxSeconds, false)
	if err != nil {
		return printErr("Invalid cache duration", err)
	}
	swr, err := cacheDurationSeconds("stale-while-revalidate", *staleWhileRevalidate, api.ResponseCacheStaleWhileRevalidateMaxSeconds, true)
	if err != nil {
		return printErr("Invalid stale-while-revalidate duration", err)
	}
	staleError, err := cacheDurationSeconds("stale-if-error", *staleIfError, api.ResponseCacheStaleIfErrorMaxSeconds, true)
	if err != nil {
		return printErr("Invalid stale-if-error duration", err)
	}

	slug, err := resolveAppFlagOrContext(strings.TrimSpace(*appFlag))
	if err != nil {
		return printErr("Could not resolve app slug", fmt.Errorf("pass --app <slug> or run inside a linked project: %w", err))
	}
	if !validCLISlug(slug) {
		return printErr("Invalid app slug", fmt.Errorf("%q is not a valid app slug", slug))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	host := strings.TrimSpace(*hostFlag)
	if host == "" {
		app, getErr := client.GetApp(context.Background(), slug)
		if getErr != nil {
			return printErr("Could not resolve app hostname", getErr)
		}
		host, err = cacheHostFromApp(app)
		if err != nil {
			return printErr("Could not resolve app hostname", err)
		}
	} else {
		host, err = normalizeCacheHost(host)
		if err != nil {
			return printErr("Invalid cache host", err)
		}
	}

	cacheAction := api.EdgeRuleCacheAction{
		MaxAgeSeconds:               maxAge,
		StaleWhileRevalidateSeconds: swr,
		StaleIfErrorSeconds:         staleError,
		VaryOn:                      varyOn,
		Methods:                     []string{method},
	}
	if problem := cacheAction.Validate(); problem != nil {
		return printErr("Invalid cache declaration", errToError(problem))
	}
	action, err := json.Marshal(cacheAction)
	if err != nil {
		return printErr("Could not encode cache declaration", err)
	}
	out, err := client.CreateEdgeRule(context.Background(), slug, api.CreateEdgeRuleRequest{
		MatchHost:    host,
		MatchPath:    matchPath,
		MatchMethods: []string{method},
		Priority:     priority,
		Enabled:      boolPtr(true),
		Kind:         "cache",
		Action:       action,
	})
	if err != nil {
		return printErr("Cache declaration failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Caching %s %s for %s on %s (%s).", method, positional[1], time.Duration(maxAge)*time.Second, slug, host)
	return 0
}

const (
	httpMethodGet  = "GET"
	httpMethodHead = "HEAD"
)

func cacheDurationSeconds(label, raw string, maxSeconds int, allowZero bool) (int, error) {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a duration such as 30s or 5m", label, raw)
	}
	if d < 0 || (!allowZero && d == 0) || d%time.Second != 0 {
		minimum := "at least one"
		if allowZero {
			minimum = "zero or more"
		}
		return 0, fmt.Errorf("%s must be %s whole seconds", label, minimum)
	}
	if d > time.Duration(maxSeconds)*time.Second {
		return 0, fmt.Errorf("%s must not exceed %s", label, time.Duration(maxSeconds)*time.Second)
	}
	return int(d / time.Second), nil
}

// cacheRoutePattern turns framework-style route parameters into the path.Match
// glob the gateway stores. `/products/:id` therefore matches one product path
// segment without making developers learn the lower-level edge-rule syntax.
func cacheRoutePattern(route string) (string, error) {
	if route == "" || !strings.HasPrefix(route, "/") {
		return "", errors.New("path must start with /")
	}
	if strings.ContainsAny(route, "?#") {
		return "", errors.New("path must not include a query string or fragment")
	}
	segments := strings.Split(route, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			if len(segment) == 1 {
				return "", errors.New("route parameter names cannot be empty")
			}
			segments[i] = "*"
		}
	}
	pattern := strings.Join(segments, "/")
	if _, err := path.Match(pattern, "/"); err != nil {
		return "", fmt.Errorf("invalid path pattern: %w", err)
	}
	return pattern, nil
}

func cacheHostFromApp(app api.AppResponse) (string, error) {
	raw := strings.TrimSpace(app.CanonicalURL)
	if raw == "" {
		raw = strings.TrimSpace(app.URL)
	}
	if raw == "" {
		return "", errors.New("app has no canonical or platform URL; pass --host")
	}
	return normalizeCacheHost(raw)
}

func normalizeCacheHost(raw string) (string, error) {
	candidate := strings.TrimSpace(raw)
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	u, err := url.Parse(candidate)
	if err != nil || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q is not a hostname or URL", raw)
	}
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), nil
}
