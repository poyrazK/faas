package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/state"
)

const EnvironmentQualificationServicePrefix = api.EnvironmentQualificationServicePrefix

type EnvironmentQualificationServiceProxyConfig struct {
	Store state.EnvironmentQualificationServiceStore
	// ResolveNode derives the actual node UUID from a fresh lookup of the
	// observed source IP on this listener's node. It never reads guest headers.
	ResolveNode func(context.Context, string) (string, error)
	Forward     func(Target) http.Handler
	Next        http.Handler
}

// This listener route has no live-endpoint cache, wake, retries or release
// fallback. Every dependency call rechecks both original execution frames.
type environmentQualificationServiceProxy struct {
	config EnvironmentQualificationServiceProxyConfig
}

func NewEnvironmentQualificationServiceProxy(config EnvironmentQualificationServiceProxyConfig) http.Handler {
	return &environmentQualificationServiceProxy{config: config}
}

func (p *environmentQualificationServiceProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip, valid := qualificationCallerAddress(r.RemoteAddr)
	if !valid || p.config.Store == nil || p.config.ResolveNode == nil {
		serviceProxyProblem(w, http.StatusForbidden, "private service caller identity is unavailable")
		return
	}
	nodeID, err := p.config.ResolveNode(r.Context(), ip.String())
	if err != nil {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "private service caller lookup is unavailable")
		return
	}
	private := strings.HasPrefix(r.URL.Path, EnvironmentQualificationServicePrefix)
	if nodeID == "" {
		if !private && p.config.Next != nil {
			p.config.Next.ServeHTTP(w, r)
			return
		}
		serviceProxyProblem(w, http.StatusForbidden, "private service caller identity is unknown")
		return
	}
	held, err := p.config.Store.EnvironmentQualificationNetworkCaller(r.Context(), nodeID, ip.String())
	if err != nil {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "private service authority is unavailable")
		return
	}
	if !held && !private && p.config.Next != nil {
		p.config.Next.ServeHTTP(w, r)
		return
	}
	if !held || !private {
		serviceProxyProblem(w, http.StatusForbidden, "qualification callers require their private graph binding")
		return
	}
	p.servePrivate(w, r, nodeID, ip.String())
}

func qualificationCallerAddress(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip, err := netip.ParseAddr(host)
	return ip, err == nil && ip.Is4()
}

func (p *environmentQualificationServiceProxy) servePrivate(w http.ResponseWriter, r *http.Request, nodeID, hostIP string) {
	graphID, binding, path, ok := parseEnvironmentQualificationServicePath(r.URL)
	if !ok || isUpgradeRequest(r) || p.config.Forward == nil {
		serviceProxyProblem(w, http.StatusForbidden, "private service request is unsupported")
		return
	}
	route, err := p.config.Store.ResolveEnvironmentQualificationService(r.Context(), state.EnvironmentQualificationServiceRequest{NodeID: nodeID, HostIP: hostIP, GraphID: graphID, Binding: binding})
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrNotFound) {
			status = http.StatusForbidden
		}
		serviceProxyProblem(w, status, "private graph dependency is unavailable")
		return
	}
	if !time.Now().Before(route.Deadline) || route.RequireHTTPS && r.TLS == nil || route.CallScope != nil && !route.CallScope.Allows(r.Method, path) {
		serviceProxyProblem(w, http.StatusForbidden, "private graph dependency policy denied the request")
		return
	}
	// Private execution authority is a hard deadline. An ordinary streaming
	// budget may detach after response headers; it must never detach this lease.
	ctx, cancel := context.WithDeadline(reqbudget.WithoutBudget(r.Context()), route.Deadline)
	defer cancel()
	stop := p.watchPrivateRoute(ctx, cancel, state.EnvironmentQualificationServiceRequest{NodeID: nodeID, HostIP: hostIP, GraphID: graphID, Binding: binding}, route)
	defer stop()
	request := privateQualificationRequest(ctx, r, route.Target, path)
	target := route.Target
	p.config.Forward(Target{AppID: target.AppID, NodeID: target.NodeID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, WakeID: target.WakeID, Port: route.Port}).ServeHTTP(w, request)
}

func privateQualificationRequest(ctx context.Context, r *http.Request, target state.EnvironmentQualificationExecution, path string) *http.Request {
	request := r.Clone(ctx)
	request.URL.Path, request.URL.RawPath = path, ""
	request.RequestURI = request.URL.RequestURI()
	request.Host = strings.TrimPrefix(target.Resource, "workload/") + ".qualification.gregale.invalid"
	for name := range request.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-faas-") || strings.HasPrefix(strings.ToLower(name), "x-gregale-") || strings.EqualFold(name, "Forwarded") || strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
			delete(request.Header, name)
		}
	}
	request.Header.Set("x-faas-instance", target.InstanceID)
	request.Header.Set("x-faas-app", target.AppID)
	request.Header.Set("x-faas-wake-id", target.WakeID)
	request.Header.Set("x-faas-protocol", "http1")
	return request
}

// Revocation cancels in-flight streams as well as denying the next call. The
// original lease deadline cannot be extended by a concurrent renewal.
func (p *environmentQualificationServiceProxy) watchPrivateRoute(ctx context.Context, cancel context.CancelFunc, request state.EnvironmentQualificationServiceRequest, original state.EnvironmentQualificationServiceRoute) func() {
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		ticker := time.NewTicker(api.EnvironmentGitOpsQualificationRuntimeCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				current, err := p.config.Store.ResolveEnvironmentQualificationService(ctx, request)
				if err != nil || current.Caller != original.Caller || current.Target != original.Target || current.Port != original.Port || current.RequireHTTPS != original.RequireHTTPS || !reflect.DeepEqual(current.CallScope, original.CallScope) || !time.Now().Before(current.Deadline) {
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(done); cancel(); <-exited }
}

func parseEnvironmentQualificationServicePath(u *url.URL) (string, string, string, bool) {
	if u == nil || !strings.HasPrefix(u.Path, EnvironmentQualificationServicePrefix) {
		return "", "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, EnvironmentQualificationServicePrefix), "/", 3)
	if len(parts) < 2 || parts[0] == "" || !api.ValidAppSlug(parts[1]) {
		return "", "", "", false
	}
	path := "/"
	if len(parts) == 3 {
		path += parts[2]
	}
	if strings.Contains(path, "%") {
		return "", "", "", false // Never let an application decode a second route.
	}
	all := api.ServiceCallScope{Methods: []string{"*"}, PathPrefixes: []string{"/"}}
	return parts[0], parts[1], path, all.Allows(http.MethodGet, path)
}
