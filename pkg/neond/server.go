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

// Server forwards allowed Neon calls with the admin key.
type Server struct {
	neondpb.UnimplementedNeondServer
	base     *url.URL
	client   *http.Client
	adminKey string
	policy   *Policy
	log      *slog.Logger
	requests *prometheus.CounterVec
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
			Help: "Neon API calls apid asked neond to forward, by route, key tier and decision (ADR-20261112160000).",
		}, []string{"route", "tier", "decision"})}
	s.policy = NewPolicy(live, s.readBranchDefault, destructivePerHour, nil)
	if reg != nil {
		if err := reg.Register(s.requests); err != nil {
			return nil, err
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
	attrs := []any{"route", route.Name, "tier", route.Tier, "project", params.Project, "branch", params.Branch}
	if err := s.policy.Check(ctx, route, params, req.GetPath()); err != nil {
		switch {
		case errors.Is(err, ErrRefused):
			s.requests.WithLabelValues(route.Name, string(route.Tier), "refused").Inc()
			s.log.Warn("neond: call refused", append(attrs, "reason", err.Error())...)
			return nil, status.Error(codes.PermissionDenied, "neond: refused by policy")
		case errors.Is(err, ErrRateLimited):
			s.requests.WithLabelValues(route.Name, string(route.Tier), "rate_limited").Inc()
			s.log.Error("neond: destructive call rate limit reached", attrs...)
			return nil, status.Error(codes.ResourceExhausted, "neond: destructive call rate limit reached")
		default:
			s.requests.WithLabelValues(route.Name, string(route.Tier), "error").Inc()
			s.log.Warn("neond: policy check failed", append(attrs, "err", err)...)
			return nil, status.Error(codes.Unavailable, "neond: policy check unavailable")
		}
	}
	code, body, retryAfter, err := s.call(ctx, req.GetMethod(), req.GetPath(), req.GetRawQuery(), req.GetBody())
	if err != nil {
		s.requests.WithLabelValues(route.Name, string(route.Tier), "error").Inc()
		s.log.Warn("neond: Neon unreachable", append(attrs, "err", err)...)
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, status.Error(codes.Unavailable, "neond: Neon unavailable")
	}
	s.requests.WithLabelValues(route.Name, string(route.Tier), "forwarded").Inc()
	s.log.Info("neond: forwarded", append(attrs, "status", code, "duration_ms", time.Since(started).Milliseconds())...)
	return &neondpb.ForwardResponse{Status: int32(code), Body: body, RetryAfter: retryAfter}, nil //nolint:gosec // HTTP status codes fit int32
}

// call sends one request to Neon with the admin key and reads at most
// MaxResponseBytes+1 of the reply, so the caller sees an oversized body.
func (s *Server) call(ctx context.Context, method, path, rawQuery string, body []byte) (int, []byte, string, error) {
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
	request.Header.Set("Authorization", "Bearer "+s.adminKey)
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
	code, body, _, err := s.call(ctx, http.MethodGet, "/projects/"+project+"/branches/"+branch, "", nil)
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
