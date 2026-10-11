package profiling

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Principal is stamped by vmmd from a live instance. The DAC-protected socket
// authenticates root peers before gRPC accepts host-owned identity.
type Principal struct {
	Routes       []string  `json:"routes,omitempty"`
	AccountID    string    `json:"account_id"`
	AppID        string    `json:"app_id"`
	DeploymentID string    `json:"deployment_id"`
	InstanceID   string    `json:"instance_id"`
	Generation   string    `json:"generation"`
	Scope        string    `json:"scope"`
	Runtime      string    `json:"runtime"`
	Plan         api.Plan  `json:"plan"`
	StartedAt    time.Time `json:"started_at"`
}

// Upload is the guest's contribution. It contains no tenant or deployment
// identity. Times are only fallbacks for SDKs with unset pprof timestamps.
type Upload = profileproto.Upload

type Envelope struct {
	Principal Principal `json:"principal"`
	Upload    Upload    `json:"upload"`
}

type rawSampleIdentityKey struct{}

type sampleIdentity struct {
	ID         string
	Collector  string
	ReceivedAt time.Time
}

type accountWindow struct {
	minute  int64
	count   int
	touched time.Time
}
type Service struct {
	Backend    Backend
	clock      func() time.Time
	slots      chan struct{}
	mu         sync.Mutex
	accounts   map[string]accountWindow
	accepted   *prometheus.CounterVec
	duplicates map[string]time.Time
	inflight   map[string]bool
	failures   map[string]time.Time
}

func NewService(backend Backend, registry prometheus.Registerer) *Service {
	s := &Service{Backend: backend, clock: time.Now, slots: make(chan struct{}, api.ProfileMaxConcurrentUploads), accounts: map[string]accountWindow{}, duplicates: map[string]time.Time{}, inflight: map[string]bool{}, failures: map[string]time.Time{}}
	s.accepted = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gregale_profile_uploads_total", Help: "CPU profile upload outcomes."}, []string{"result"})
	if registry != nil {
		registry.MustRegister(s.accepted)
	}
	for _, result := range []string{"accepted", "duplicate", "invalid", "limited", "unavailable"} {
		s.accepted.WithLabelValues(result)
	}
	return s
}

func validPrincipal(p Principal) bool {
	if len(p.Routes) > api.ProfileRouteMaxLabels {
		return false
	}
	for _, r := range p.Routes {
		if !api.ValidProfileRoute(r) || r == "" || r == api.ProfileUnattributedRoute {
			return false
		}
	}
	for _, id := range []string{p.AccountID, p.AppID, p.DeploymentID, p.InstanceID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	if p.StartedAt.IsZero() || p.Generation == "" || len(p.Generation) > api.ProfileMaxGenerationBytes || api.ValidateScope(p.Scope) != nil {
		return false
	}
	return ValidRuntime(p.Runtime)
}

func (s *Service) admit(account string, limit int, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, w := range s.accounts {
		if now.Sub(w.touched) > api.ProfileRetryCacheTTL {
			delete(s.accounts, key)
		}
	}
	for key, at := range s.duplicates {
		if now.Sub(at) > api.ProfileRetryCacheTTL {
			delete(s.duplicates, key)
		}
	}
	w, exists := s.accounts[account]
	if !exists && len(s.accounts) >= api.ProfileMaxTrackedAccounts {
		return false
	}
	minute := now.Unix() / 60
	if w.minute != minute {
		w.count = 0
		w.minute = minute
	}
	if w.count >= limit {
		return false
	}
	w.count++
	w.touched = now
	s.accounts[account] = w
	return true
}

func (s *Service) Push(ctx context.Context, e Envelope) (result error) {
	if !validPrincipal(e.Principal) {
		return status.Error(codes.InvalidArgument, "invalid profile principal")
	}
	limits, ok := api.LimitsFor(e.Principal.Plan)
	if !ok || !limits.Profiling.Enabled {
		return status.Error(codes.PermissionDenied, "profiling is disabled")
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		s.accepted.WithLabelValues("limited").Inc()
		return status.Error(codes.ResourceExhausted, "profile service busy")
	}
	// Best-effort failure evidence shares the existing upload slot and caller
	// deadline. Busy slots and failures before ingestion remain unobserved.
	defer func() {
		if result != nil {
			s.recordFailure(ctx, e.Principal)
		}
	}()
	if !s.admit(e.Principal.AccountID, limits.Profiling.UploadsPerMinute, s.clock()) {
		s.accepted.WithLabelValues("limited").Inc()
		return status.Error(codes.ResourceExhausted, "profile upload quota exceeded")
	}
	return s.push(ctx, e)
}

func (s *Service) push(ctx context.Context, e Envelope) error {
	if e.Upload.ProcessID != "" {
		pid, err := strconv.ParseUint(e.Upload.ProcessID, 10, 32)
		if err != nil || pid == 0 {
			return status.Error(codes.InvalidArgument, "invalid profile process identity")
		}
		e.Upload.ProcessID = strconv.FormatUint(pid, 10)
	}
	p, err := Parse(e.Upload.Profile)
	if err == nil {
		reasons := sanitizeRouteSamples(p, e.Principal.Routes)
		ctx = context.WithValue(ctx, routeReasonsKey{}, reasons)
		err = normalizeCPU(p, true)
	}
	if err != nil {
		s.accepted.WithLabelValues("invalid").Inc()
		return status.Error(codes.InvalidArgument, "invalid CPU profile")
	}
	if p.TimeNanos == 0 {
		p.TimeNanos = e.Upload.FromUnixNano
	}
	if p.DurationNanos == 0 && e.Upload.UntilUnixNano > p.TimeNanos {
		p.DurationNanos = e.Upload.UntilUnixNano - p.TimeNanos
	}
	start := time.Unix(0, p.TimeNanos)
	duration := time.Duration(p.DurationNanos)
	if p.TimeNanos <= 0 || duration <= 0 || duration > api.ProfileMaxCaptureDuration || start.Before(e.Principal.StartedAt) || start.Add(duration).After(s.clock().Add(time.Second)) {
		s.accepted.WithLabelValues("invalid").Inc()
		return status.Error(codes.InvalidArgument, "profile window crosses its instance lifetime or exceeds bounds")
	}
	if report := admitRouteRequestReport(e.Upload.RouteRequests, e.Principal, p); report != nil && report.Until <= s.clock().Add(time.Second).UnixNano() {
		ctx = context.WithValue(ctx, routeRequestReportKey{}, report)
	}
	// Avoid replaying SDK retries within the same VM generation. The set has
	// a hard cap and TTL, independent of tenant-provided labels.
	hash := sha256.Sum256(append(append([]byte(nil), e.Upload.Profile...), []byte(fmt.Sprintf(":%d:%d", p.TimeNanos, p.DurationNanos))...))
	id := e.Principal.InstanceID + ":" + e.Principal.Generation + ":" + e.Upload.ProcessID + ":" + hex.EncodeToString(hash[:])
	s.mu.Lock()
	if s.inflight[id] {
		s.mu.Unlock()
		return status.Error(codes.Unavailable, "profile upload in progress")
	}
	_, seen := s.duplicates[id]
	if !seen && len(s.duplicates) < api.ProfileMaxTrackedAccounts {
		s.duplicates[id] = s.clock()
		s.inflight[id] = true
	} else if !seen {
		s.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "profile retry cache is full")
	}
	s.mu.Unlock()
	if seen {
		s.accepted.WithLabelValues("duplicate").Inc()
		return nil
	}
	exported := false
	defer func() {
		s.mu.Lock()
		delete(s.inflight, id)
		if !exported {
			delete(s.duplicates, id)
		}
		s.mu.Unlock()
	}()
	if s.Backend == nil {
		s.accepted.WithLabelValues("unavailable").Inc()
		return status.Error(codes.Unavailable, "profile backend unavailable")
	}
	identity := sampleIdentity{
		ID:         uuid.NewSHA1(uuid.NameSpaceOID, []byte(id)).String(),
		Collector:  uuid.NewSHA1(uuid.NameSpaceOID, []byte(e.Principal.InstanceID+":"+e.Principal.Generation+":"+e.Upload.ProcessID)).String(),
		ReceivedAt: s.clock(),
	}
	ctx = context.WithValue(ctx, rawSampleIdentityKey{}, identity)
	if err := s.Backend.Push(ctx, e.Principal, p); err != nil {
		s.accepted.WithLabelValues("unavailable").Inc()
		return status.Error(codes.Unavailable, "profile backend unavailable")
	}
	exported = true
	s.accepted.WithLabelValues("accepted").Inc()
	return nil
}

// The internal unary RPC uses well-known protobuf messages. BytesValue holds
// the JSON Envelope; transport limits apply before JSON decoding.
const PushMethod = "/onebox.faas.profiles.v1.Ingest/Push"

type ingestRPC interface {
	PushRPC(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error)
}

func (s *Service) PushRPC(ctx context.Context, req *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	if len(req.Value) > api.ProfileMaxFrameBytes {
		return nil, status.Error(codes.ResourceExhausted, "profile frame exceeds limit")
	}
	var e Envelope
	if err := json.Unmarshal(req.Value, &e); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid profile envelope")
	}
	if err := s.Push(ctx, e); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func Register(server *grpc.Server, service *Service) {
	server.RegisterService(&grpc.ServiceDesc{ServiceName: "onebox.faas.profiles.v1.Ingest", HandlerType: (*ingestRPC)(nil), Methods: []grpc.MethodDesc{{MethodName: "Push", Handler: func(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		var req wrapperspb.BytesValue
		if err := decode(&req); err != nil {
			return nil, err
		}
		handler := func(ctx context.Context, in any) (any, error) {
			return srv.(ingestRPC).PushRPC(ctx, in.(*wrapperspb.BytesValue))
		}
		if interceptor == nil {
			return handler(ctx, &req)
		}
		return interceptor(ctx, &req, &grpc.UnaryServerInfo{Server: srv, FullMethod: PushMethod}, handler)
	}}}}, service)
}

func Send(ctx context.Context, conn grpc.ClientConnInterface, e Envelope) error {
	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode profile envelope: %w", err)
	}
	if len(body) > api.ProfileMaxFrameBytes {
		return fmt.Errorf("profile frame exceeds limit")
	}
	return conn.Invoke(ctx, PushMethod, wrapperspb.Bytes(body), &emptypb.Empty{})
}

func ValidRuntime(runtime string) bool {
	if runtime == "" || len(runtime) > api.ProfileMaxRuntimeBytes {
		return false
	}
	for _, r := range runtime {
		allowed := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !allowed {
			return false
		}
	}
	return true
}
