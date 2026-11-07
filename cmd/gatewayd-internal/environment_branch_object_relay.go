// adr: 20261031193635
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	// environmentBranchObjectRelayUpstreamEnv names the node's s3-gatewayd
	// data listener; every compute node runs one on loopback
	// (ADR-20261031122757).
	environmentBranchObjectRelayUpstreamEnv     = "FAAS_GATEWAYD_OBJECT_RELAY_UPSTREAM"
	environmentBranchObjectRelayDefaultUpstream = "127.0.0.1:8084"
	environmentBranchObjectRelayLookupTimeout   = 2 * time.Second
	// uploadGrantQueryParameter mirrors s3gateway.UploadGrantQueryParameter:
	// a grant upload carries no key, so the relay has nothing to check.
	uploadGrantQueryParameter = "gregale-upload"
)

type environmentBranchObjectRelayStore interface {
	state.EnvironmentBranchEgressStore
	state.EnvironmentBranchObjectRelayStore
}

// environmentBranchObjectRelay is the bridge object relay
// (ADR-20261031193635 slice 6c). A contained branch guest reaches object
// storage only here: the relay admits a request signed with a key of one of
// its own branch's overlay buckets and forwards it, unchanged, to the node's
// s3-gatewayd, which verifies the signature against the Host the guest used.
// A production key compiled into the build is refused before any byte
// leaves the node.
type environmentBranchObjectRelay struct {
	resolve func(ctx context.Context, remote string) (appID, deploymentID string, err error)
	store   environmentBranchObjectRelayStore
	proxy   http.Handler
	log     *slog.Logger
}

func newEnvironmentBranchObjectRelay(resolve func(context.Context, string) (string, string, error), store environmentBranchObjectRelayStore, upstream string, log *slog.Logger) (*environmentBranchObjectRelay, error) {
	if strings.TrimSpace(upstream) == "" {
		upstream = environmentBranchObjectRelayDefaultUpstream
	}
	host, _, err := net.SplitHostPort(upstream)
	if err != nil {
		return nil, fmt.Errorf("object relay upstream %q: %w", upstream, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		// s3-gatewayd accepts the relay host only from a loopback peer.
		return nil, fmt.Errorf("object relay upstream %q must be a loopback address", upstream)
	}
	target := &url.URL{Scheme: "http", Host: upstream}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// Keep the Host the guest signed; SetURL would replace it.
			r.Out.Host = r.In.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("environment branch object relay upstream failed", "err", err)
			writeObjectRelayError(w, http.StatusBadGateway, "ServiceUnavailable", "Gregale Object Storage is unavailable on this node.")
		},
	}
	return &environmentBranchObjectRelay{resolve: resolve, store: store, proxy: proxy, log: log}, nil
}

func (relay *environmentBranchObjectRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), environmentBranchObjectRelayLookupTimeout)
	defer cancel()
	status, code, err := relay.admit(ctx, r)
	if err != nil {
		relay.log.Warn("environment branch object relay refused a request", "remote", r.RemoteAddr, "code", code, "err", err)
		message := "Only this environment branch's own object storage keys are accepted here."
		if status == http.StatusServiceUnavailable {
			message = "Gregale could not check this request; try again."
		}
		writeObjectRelayError(w, status, code, message)
		return
	}
	relay.proxy.ServeHTTP(w, r)
}

var errObjectRelayRefused = errors.New("object relay refused the request")

// admit returns the refusal status and S3 error code, or a nil error.
func (relay *environmentBranchObjectRelay) admit(ctx context.Context, r *http.Request) (int, string, error) {
	_, deploymentID, err := relay.resolve(ctx, r.RemoteAddr)
	if err != nil {
		return http.StatusServiceUnavailable, "ServiceUnavailable", fmt.Errorf("resolve caller: %w", err)
	}
	if deploymentID == "" {
		return http.StatusForbidden, "AccessDenied", fmt.Errorf("%w: unknown caller", errObjectRelayRefused)
	}
	policy, err := relay.store.EnvironmentBranchEgress(ctx, deploymentID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return http.StatusForbidden, "AccessDenied", fmt.Errorf("%w: deployment %s: %w", errObjectRelayRefused, deploymentID, err)
		}
		return http.StatusServiceUnavailable, "ServiceUnavailable", fmt.Errorf("branch egress of %s: %w", deploymentID, err)
	}
	if !policy.Contained() {
		// Live branches and ordinary apps use the public endpoint.
		return http.StatusForbidden, "AccessDenied", fmt.Errorf("%w: deployment %s is not in a contained branch", errObjectRelayRefused, deploymentID)
	}
	if r.URL.Query().Has(uploadGrantQueryParameter) {
		return http.StatusForbidden, "AccessDenied", fmt.Errorf("%w: upload grants carry no key", errObjectRelayRefused)
	}
	key := objectRelayAccessKeyID(r)
	if key == "" {
		return http.StatusForbidden, "AccessDenied", fmt.Errorf("%w: unsigned request", errObjectRelayRefused)
	}
	allowed, err := relay.store.EnvironmentBranchObjectCredentialAllowed(ctx, policy.EnvironmentID, key)
	if err != nil {
		return http.StatusServiceUnavailable, "ServiceUnavailable", fmt.Errorf("check key: %w", err)
	}
	if !allowed {
		return http.StatusForbidden, "InvalidAccessKeyId", fmt.Errorf("%w: key is not one of the branch's buckets", errObjectRelayRefused)
	}
	return 0, "", nil
}

// objectRelayAccessKeyID returns the access key ID of a SigV4 request, from
// its Authorization header or its presigned query. A request carrying both
// is refused: the gateway would check one and the relay the other.
func objectRelayAccessKeyID(r *http.Request) string {
	header := sigV4HeaderAccessKeyID(r.Header.Get("Authorization"))
	query := sigV4CredentialAccessKeyID(r.URL.Query().Get("X-Amz-Credential"))
	switch {
	case header != "" && query != "":
		return ""
	case header != "":
		return header
	default:
		return query
	}
}

func sigV4HeaderAccessKeyID(authorization string) string {
	const scheme = "AWS4-HMAC-SHA256 "
	if !strings.HasPrefix(authorization, scheme) {
		return ""
	}
	for _, part := range strings.Split(strings.TrimPrefix(authorization, scheme), ",") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(part), "Credential="); ok {
			return sigV4CredentialAccessKeyID(value)
		}
	}
	return ""
}

func sigV4CredentialAccessKeyID(credential string) string {
	key, _, ok := strings.Cut(credential, "/")
	if !ok {
		return ""
	}
	return strings.TrimSpace(key)
}

// startEnvironmentBranchObjectRelay listens on the bridge object relay port.
// netns admits it only from contained branch guests; every other caller is
// refused here too, since it is not in a contained branch.
func startEnvironmentBranchObjectRelay(deps runDeps, bridgeIP netip.Addr, resolve func(context.Context, string) (string, string, error), addSrv func(*http.Server) *wire.HTTPDrain, errc chan<- error, log *slog.Logger) error {
	relay, err := newEnvironmentBranchObjectRelay(resolve, deps.pgStore, osGetenv(environmentBranchObjectRelayUpstreamEnv), log)
	if err != nil {
		return fmt.Errorf("gatewayd: %w", err)
	}
	addr := net.JoinHostPort(bridgeIP.String(), strconv.Itoa(api.EnvironmentBranchObjectRelayPort))
	srv := deps.newSrv(addr, relay)
	srv.Addr = addr
	// Object bodies use the customer request envelope, like the service
	// proxy; s3-gatewayd bounds each transfer itself.
	srv.ReadTimeout = readTimeoutOrDefault(deps.readTimeout)
	srv.WriteTimeout = writeTimeoutOrDefault(deps.writeTimeout)
	drain := addSrv(srv)
	listener, err := deps.listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gatewayd: environment branch object relay listen %s: %w", addr, err)
	}
	go func() {
		log.Info("gatewayd environment branch object relay listening", "addr", addr)
		if err := drain.ServeErr(srv.Serve(drain.Listener(listener))); err != nil {
			errc <- err
		}
	}()
	return nil
}

func writeObjectRelayError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, message)
}
