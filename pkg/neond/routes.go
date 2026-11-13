// Package neond is the holder of Gregale's Neon API keys
// (ADR-20261112160000): the allow-list of Neon calls apid's Neon backend may
// forward, the policy that refuses destroying a live database, the gRPC
// server cmd/neond runs and the client the backend uses.
package neond

import (
	"net/http"
	"regexp"
	"strings"
)

// Tier is the key a route is called with. Until project keys exist (slice
// 2) every route uses the admin key; the tier says which it will be.
type Tier string

const (
	// TierProject routes act on one Neon project and will use its
	// project-scoped key.
	TierProject Tier = "project"
	// TierAdmin routes create, list or delete projects or read
	// organization-wide consumption: only the admin key can.
	TierAdmin Tier = "admin"
)

// Kind classifies a route for the policy and the rate limit.
type Kind string

const (
	KindRead        Kind = "read"
	KindWrite       Kind = "write"
	KindDestructive Kind = "destructive"
)

// Route is one allowed method and path shape. A path segment in braces is an
// ID: {project}, {branch}, or another resource's.
type Route struct {
	Name     string
	Method   string
	Template string
	Tier     Tier
	Kind     Kind
}

// Params are the IDs a matched path names.
type Params struct {
	Project, Branch string
}

// Routes are the Neon calls the Neon backend makes (pkg/managedpostgres/neon;
// its tests fail when it sends one this list does not admit).
var Routes = []Route{
	{"list projects", http.MethodGet, "/projects", TierAdmin, KindRead},
	{"create project", http.MethodPost, "/projects", TierAdmin, KindWrite},
	{"read project", http.MethodGet, "/projects/{project}", TierProject, KindRead},
	{"update project", http.MethodPatch, "/projects/{project}", TierProject, KindWrite},
	{"delete project", http.MethodDelete, "/projects/{project}", TierAdmin, KindDestructive},
	{"read connection uri", http.MethodGet, "/projects/{project}/connection_uri", TierProject, KindRead},
	{"list operations", http.MethodGet, "/projects/{project}/operations", TierProject, KindRead},
	{"read operation", http.MethodGet, "/projects/{project}/operations/{operation}", TierProject, KindRead},
	{"list branches", http.MethodGet, "/projects/{project}/branches", TierProject, KindRead},
	{"create branch", http.MethodPost, "/projects/{project}/branches", TierProject, KindWrite},
	{"read branch", http.MethodGet, "/projects/{project}/branches/{branch}", TierProject, KindRead},
	{"delete branch", http.MethodDelete, "/projects/{project}/branches/{branch}", TierProject, KindDestructive},
	{"snapshot branch", http.MethodPost, "/projects/{project}/branches/{branch}/snapshot", TierProject, KindWrite},
	{"delete role", http.MethodDelete, "/projects/{project}/branches/{branch}/roles/{role}", TierProject, KindDestructive},
	{"list endpoints", http.MethodGet, "/projects/{project}/endpoints", TierProject, KindRead},
	{"create endpoint", http.MethodPost, "/projects/{project}/endpoints", TierProject, KindWrite},
	{"read endpoint", http.MethodGet, "/projects/{project}/endpoints/{endpoint}", TierProject, KindRead},
	{"update endpoint", http.MethodPatch, "/projects/{project}/endpoints/{endpoint}", TierProject, KindWrite},
	{"delete endpoint", http.MethodDelete, "/projects/{project}/endpoints/{endpoint}", TierProject, KindDestructive},
	{"list snapshots", http.MethodGet, "/projects/{project}/snapshots", TierProject, KindRead},
	{"update snapshot", http.MethodPatch, "/projects/{project}/snapshots/{snapshot}", TierProject, KindWrite},
	{"delete snapshot", http.MethodDelete, "/projects/{project}/snapshots/{snapshot}", TierProject, KindDestructive},
	{"restore snapshot", http.MethodPost, "/projects/{project}/snapshots/{snapshot}/restore", TierProject, KindWrite},
	{"read consumption", http.MethodGet, "/consumption_history/v2/projects", TierAdmin, KindRead},
}

// validID is a Neon resource ID as a path segment: no escapes, dots or
// slashes, so a matched path can only name what its template says.
var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// Match finds the route a request takes. A path that is not on the
// allow-list, or names an ID in a shape Neon never issues, matches nothing.
func Match(method, path string) (Route, Params, bool) {
	if !strings.HasPrefix(path, "/") || len(path) > 512 {
		return Route{}, Params{}, false
	}
	segments := strings.Split(path[1:], "/")
	for _, route := range Routes {
		if route.Method != method {
			continue
		}
		if params, ok := matchTemplate(route.Template, segments); ok {
			return route, params, true
		}
	}
	return Route{}, Params{}, false
}

func matchTemplate(template string, segments []string) (Params, bool) {
	parts := strings.Split(template[1:], "/")
	if len(parts) != len(segments) {
		return Params{}, false
	}
	var params Params
	for i, part := range parts {
		segment := segments[i]
		if !strings.HasPrefix(part, "{") {
			if part != segment {
				return Params{}, false
			}
			continue
		}
		if !validID.MatchString(segment) {
			return Params{}, false
		}
		switch part {
		case "{project}":
			params.Project = segment
		case "{branch}":
			params.Branch = segment
		}
	}
	return params, true
}
