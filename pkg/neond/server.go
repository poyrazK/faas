package neond

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	neondpb "github.com/onebox-faas/faas/api/proto/onebox/faas/neond/v1"
)

// MaxRequestBytes and MaxResponseBytes bound a forwarded body. The Neon
// backend never sends or reads more (its own limit is 4 MiB).
const (
	MaxRequestBytes  = 1 << 20
	MaxResponseBytes = 4 << 20
)

// Server forwards allowed Neon calls: a project's routes with its project
// key once UseProjectKeys is on and the project has one, the rest with the
// admin key.
type Server struct {
	neondpb.UnimplementedNeondServer
	base     *url.URL
	client   *http.Client
	adminKey string
	policy   *Policy
	keys     *projectKeys
	log      *slog.Logger
	requests *prometheus.CounterVec
	keyOps   *prometheus.CounterVec
}

// NewServer builds the server. base is Neon's API base
// (https://console.neon.tech/api/v2); client must not follow redirects.
func NewServer(base *url.URL, client *http.Client, adminKey string, live LiveResources, destructivePerHour int, reg prometheus.Registerer, log *slog.Logger) (*Server, error) {
	if base == nil || client == nil || adminKey == "" || live == nil || destructivePerHour < 1 || log == nil {
		return nil, errors.New("neond: invalid server configuration")
	}
	s := &Server{base: base, client: client, adminKey: adminKey, log: log,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "neond_requests_total",
			Help: "Neon API calls apid asked neond to forward, by route, key tier and decision (ADR-20261112160000). tier is the key used: project, admin, or admin_fallback for a project route without a usable project key.",
		}, []string{"route", "tier", "decision"}),
		keyOps: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "neond_project_keys_total",
			Help: "Project keys neond minted or revoked, by outcome (ADR-20261112160000 decision 3).",
		}, []string{"op", "outcome"})}
	s.policy = NewPolicy(live, s.readBranchDefault, destructivePerHour, nil)
	if reg != nil {
		for _, c := range []prometheus.Collector{s.requests, s.keyOps} {
			if err := reg.Register(c); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

// Forward implements neondpb.NeondServer.
func (s *Server) Forward(ctx context.Context, req *neondpb.ForwardRequest) (*neondpb.ForwardResponse, error) {
	started := time.Now()
	route, params, ok := Match(req.GetMethod(), req.GetPath())
	if !ok || len(req.GetBody()) > MaxRequestBytes || strings.ContainsAny(req.GetRawQuery(), "#") {
		s.requests.WithLabelValues("unknown", "", "denied").Inc()
		s.log.Warn("neond: call not on the allow-list", "method", req.GetMethod(), "path", req.GetPath())
		return nil, status.Error(codes.PermissionDenied, "neond: call not allowed")
	}
	key, tier := s.keyFor(ctx, route, params)
	attrs := []any{"route", route.Name, "tier", tier, "project", params.Project, "branch", params.Branch}
	if err := s.policy.Check(ctx, route, params, req.GetPath()); err != nil {
		switch {
		case errors.Is(err, ErrRefused):
			s.requests.WithLabelValues(route.Name, tier, "refused").Inc()
			s.log.Warn("neond: call refused", append(attrs, "reason", err.Error())...)
			return nil, status.Error(codes.PermissionDenied, "neond: refused by policy")
		case errors.Is(err, ErrRateLimited):
			s.requests.WithLabelValues(route.Name, tier, "rate_limited").Inc()
			s.log.Error("neond: destructive call rate limit reached", attrs...)
			return nil, status.Error(codes.ResourceExhausted, "neond: destructive call rate limit reached")
		default:
			s.requests.WithLabelValues(route.Name, tier, "error").Inc()
			s.log.Warn("neond: policy check failed", append(attrs, "err", err)...)
			return nil, status.Error(codes.Unavailable, "neond: policy check unavailable")
		}
	}
	code, body, retryAfter, err := s.call(ctx, key, req.GetMethod(), req.GetPath(), req.GetRawQuery(), req.GetBody())
	if err == nil && tier == string(TierProject) && (code == http.StatusUnauthorized || code == http.StatusForbidden) {
		// A revoked or under-privileged project key: forget it and retry
		// with the admin key, so the call never fails on the key alone.
		s.log.Warn("neond: Neon refused the project key; retrying with the admin key", append(attrs, "status", code)...)
		s.keys.forget(params.Project)
		tier = "admin_fallback"
		attrs[3] = tier
		code, body, retryAfter, err = s.call(ctx, s.adminKey, req.GetMethod(), req.GetPath(), req.GetRawQuery(), req.GetBody())
	}
	if err != nil {
		s.requests.WithLabelValues(route.Name, tier, "error").Inc()
		s.log.Warn("neond: Neon unreachable", append(attrs, "err", err)...)
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, status.Error(codes.Unavailable, "neond: Neon unavailable")
	}
	s.requests.WithLabelValues(route.Name, tier, "forwarded").Inc()
	s.log.Info("neond: forwarded", append(attrs, "status", code, "duration_ms", time.Since(started).Milliseconds())...)
	s.afterForward(ctx, route, params, code, body)
	return &neondpb.ForwardResponse{Status: int32(code), Body: body, RetryAfter: retryAfter}, nil //nolint:gosec // HTTP status codes fit int32
}

// keyFor picks the key a call goes out with and names its tier.
func (s *Server) keyFor(ctx context.Context, route Route, params Params) (string, string) {
	if route.Tier != TierProject || s.keys == nil {
		return s.adminKey, string(TierAdmin)
	}
	key, ok, err := s.keys.projectKey(ctx, params.Project)
	if err != nil {
		s.log.Warn("neond: project key unavailable; using the admin key", "project", params.Project, "err", err)
	}
	if !ok || err != nil {
		return s.adminKey, "admin_fallback"
	}
	return key, string(TierProject)
}

// afterForward keeps project keys in step with projects: a created project
// gets a key before apid hears back, a deleted one loses its keys.
func (s *Server) afterForward(ctx context.Context, route Route, params Params, code int, body []byte) {
	if s.keys == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), keyOpTimeout)
	defer cancel()
	switch {
	case route.Name == "create project" && code >= 200 && code < 300:
		project, ok := createdProjectID(body)
		if !ok {
			s.keyOps.WithLabelValues("mint", "error").Inc()
			s.log.Error("neond: created project has no readable ID; it stays on the admin key")
			return
		}
		if err := s.mintProjectKey(ctx, project); err != nil {
			s.keyOps.WithLabelValues("mint", "error").Inc()
			s.log.Error("neond: project key not minted; the project stays on the admin key", "project", project, "err", err)
			return
		}
		s.keyOps.WithLabelValues("mint", "ok").Inc()
		s.log.Info("neond: project key minted", "project", project)
	case route.Name == "delete project" && (code >= 200 && code < 300 || code == http.StatusNotFound):
		if err := s.revokeProjectKeys(ctx, params.Project); err != nil {
			s.keyOps.WithLabelValues("revoke", "error").Inc()
			s.log.Error("neond: project key not revoked", "project", params.Project, "err", err)
			return
		}
		s.keyOps.WithLabelValues("revoke", "ok").Inc()
	}
}

// call sends one request to Neon with key and reads at most
// MaxResponseBytes+1 of the reply, so the caller sees an oversized body.
func (s *Server) call(ctx context.Context, key, method, path, rawQuery string, body []byte) (int, []byte, string, error) {
	endpoint := *s.base
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawPath = ""
	endpoint.RawQuery = rawQuery
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return 0, nil, "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return 0, nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return 0, nil, "", err
	}
	return response.StatusCode, payload, response.Header.Get("Retry-After"), nil
}

// readBranchDefault reads whether a branch is its project's default branch.
func (s *Server) readBranchDefault(ctx context.Context, project, branch string) (bool, error) {
	code, body, _, err := s.call(ctx, s.adminKey, http.MethodGet, "/projects/"+project+"/branches/"+branch, "", nil)
	if err != nil {
		return false, err
	}
	if code != http.StatusOK || len(body) > MaxResponseBytes {
		return false, errors.New("neond: branch read failed")
	}
	var response struct {
		Branch struct {
			ID      string `json:"id"`
			Default bool   `json:"default"`
		} `json:"branch"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Branch.ID != branch {
		return false, errors.New("neond: branch read failed")
	}
	return response.Branch.Default, nil
}
