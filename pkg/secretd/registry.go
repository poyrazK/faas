package secretd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// ADR-20261110210000 slice 3j: compute nodes and builder VMs pull private
// images through secretd. The registry password (sealed to the build seal
// domain) and the bearer tokens it buys stay here; the caller sees only the
// registry's answer.

// RegistryCredentialNamespace is the secretbox namespace apid seals
// app_registry_credentials passwords under (ADR-062).
const RegistryCredentialNamespace = "registry_creds"

// ErrUnknownRegistryCredential is returned by Store.RegistryCredential.
var ErrUnknownRegistryCredential = errors.New("secretd: the app has no credential for that registry")

const (
	// MaxConcurrentRegistryFetches bounds the pulls secretd carries at once;
	// a fetch past it waits for a slot or its caller's deadline.
	MaxConcurrentRegistryFetches = 64
	// maxRegistryRedirects bounds the redirects one fetch follows (a blob
	// GET usually redirects once, to the registry's storage).
	maxRegistryRedirects = 5
	// registryChunkBytes is the body frame size.
	registryChunkBytes = 256 << 10
	// maxRegistryURLBytes bounds a request URL.
	maxRegistryURLBytes = 2048
	// registryTokenFallbackTTL is how long a token without expires_in is
	// reused; the distribution spec's default lifetime is 60 seconds.
	registryTokenFallbackTTL = 50 * time.Second
	registryUserAgent        = "gregale-secretd"
)

// registryForwardHeaders are the only request headers secretd sends on.
var registryForwardHeaders = []string{"Accept", "Range", "If-None-Match", "If-Modified-Since"}

// registryReturnHeaders are the only response headers the caller gets.
var registryReturnHeaders = []string{"Content-Type", "Content-Length", "Docker-Content-Digest", "Etag",
	"Content-Range", "Accept-Ranges", "Last-Modified", "Docker-Distribution-Api-Version", "Link"}

// WithBuildIdentities lets secretd open registry passwords (current first,
// then previous) for RegistryFetch (ADR-20261110210000 slice 3j).
func (s *Server) WithBuildIdentities(ids []*age.X25519Identity) *Server {
	s.buildIdentities = ids
	return s
}

// WithRegistryHTTPClient replaces the upstream client (tests). Production
// uses oci.NewEgressHTTPClient, which refuses private and metadata
// addresses, so a caller cannot reach the control plane's own network.
func (s *Server) WithRegistryHTTPClient(hc *http.Client) *Server {
	s.registry = newRegistryFetcher(hc, s.now)
	return s
}

type registryTokenKey struct{ app, host, repository string }

type registryToken struct {
	authorization string
	expires       time.Time
}

// registryFetcher makes upstream registry requests with an app's
// credential and caches the bearer tokens it buys per app and repository.
type registryFetcher struct {
	hc    *http.Client
	now   func() time.Time
	slots chan struct{}
	// creds resolves stored credentials (ECR IAM keys to tokens) and
	// caches the exchanged tokens.
	creds *oci.RegistryClient

	mu     sync.Mutex
	tokens map[registryTokenKey]registryToken
}

func newRegistryFetcher(hc *http.Client, now func() time.Time) *registryFetcher {
	if hc == nil {
		hc = oci.NewEgressHTTPClient()
	}
	client := *hc
	client.Timeout = 0 // a blob streams for as long as its caller waits
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRegistryRedirects {
			return errors.New("too many registry redirects")
		}
		if req.URL.Scheme != "https" && via[0].URL.Scheme == "https" {
			return errors.New("registry redirect leaves https")
		}
		// The credential goes only to the registry's own API: storage
		// redirects (presigned URLs) never get it, on any host.
		if !strings.EqualFold(req.URL.Host, via[0].URL.Host) || !strings.HasPrefix(req.URL.Path, "/v2/") {
			req.Header.Del("Authorization")
		}
		return nil
	}
	return &registryFetcher{hc: &client, now: now, slots: make(chan struct{}, MaxConcurrentRegistryFetches),
		creds:  oci.NewRegistryClient(oci.WithHTTPClient(&client)),
		tokens: map[registryTokenKey]registryToken{}}
}

// registryTarget is a validated registry request URL.
type registryTarget struct {
	url        *url.URL
	repository string
	manifest   bool
}

// parseRegistryTarget admits only https://<host>/v2/... without user info.
// The repository is the path between /v2/ and its last /manifests/, /blobs/
// or /tags/ segment ("" for the /v2/ ping).
func parseRegistryTarget(raw string) (registryTarget, error) {
	if len(raw) > maxRegistryURLBytes {
		return registryTarget{}, errors.New("url too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return registryTarget{}, errors.New("not a registry url")
	}
	if u.Scheme != "https" {
		return registryTarget{}, errors.New("registry url must be https")
	}
	if !strings.HasPrefix(u.Path, "/v2/") || strings.Contains(u.Path, "/../") || strings.HasSuffix(u.Path, "/..") {
		return registryTarget{}, errors.New("registry url must be under /v2/")
	}
	rest := strings.TrimPrefix(u.Path, "/v2/")
	t := registryTarget{url: u}
	for _, marker := range []string{"/manifests/", "/blobs/", "/tags/"} {
		if i := strings.LastIndex(rest, marker); i > 0 {
			t.repository = rest[:i]
			t.manifest = marker == "/manifests/"
			break
		}
	}
	return t, nil
}

func repositoryKey(app, host, repository string) registryTokenKey {
	return registryTokenKey{app: app, host: strings.ToLower(host), repository: repository}
}

func (f *registryFetcher) cached(key registryTokenKey) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok, ok := f.tokens[key]
	if !ok || !f.now().Before(tok.expires) {
		delete(f.tokens, key)
		return ""
	}
	return tok.authorization
}

func (f *registryFetcher) store(key registryTokenKey, authorization string, ttl time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tokens) >= 10_000 {
		f.tokens = map[registryTokenKey]registryToken{}
	}
	f.tokens[key] = registryToken{authorization: authorization, expires: f.now().Add(ttl)}
}

// fetch sends method target with the caller's allowed headers. On a 401 it
// answers the registry's challenge with auth (Bearer through the realm, or
// Basic) and retries once.
func (f *registryFetcher) fetch(ctx context.Context, app, method string, target registryTarget, headers []*secretdpb.RegistryHeader, auth *oci.BasicAuth) (*http.Response, error) {
	select {
	case f.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	key := repositoryKey(app, target.url.Host, target.repository)
	do := func(authorization string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, target.url.String(), nil)
		if err != nil {
			return nil, err
		}
		for _, h := range headers {
			for _, allowed := range registryForwardHeaders {
				if strings.EqualFold(h.GetName(), allowed) {
					req.Header.Add(allowed, h.GetValue())
				}
			}
		}
		req.Header.Set("User-Agent", registryUserAgent)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		return f.hc.Do(req)
	}
	resp, err := do(f.cached(key))
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		drain(resp)
		authorization, ttl, aerr := f.authorize(ctx, challenge, auth)
		if aerr != nil {
			<-f.slots
			return nil, aerr
		}
		f.store(key, authorization, ttl)
		resp, err = do(authorization)
	}
	if err != nil {
		<-f.slots
		return nil, err
	}
	resp.Body = &releasingBody{ReadCloser: resp.Body, release: func() { <-f.slots }}
	return resp, nil
}

// authorize answers a WWW-Authenticate challenge with the app's credential.
func (f *registryFetcher) authorize(ctx context.Context, challenge string, auth *oci.BasicAuth) (string, time.Duration, error) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(challenge)), "basic") {
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth(auth.Username, auth.Password)
		return req.Header.Get("Authorization"), registryTokenFallbackTTL, nil
	}
	ch := oci.ParseChallenge(challenge)
	if ch.Realm() == "" {
		return "", 0, errors.New("registry asked for credentials without a bearer realm")
	}
	realm, err := url.Parse(ch.Realm())
	if err != nil || realm.Scheme != "https" && realm.Scheme != "http" {
		return "", 0, errors.New("registry token realm is not a url")
	}
	tok, err := oci.FetchToken(ctx, f.hc, registryUserAgent, ch, auth)
	if err != nil {
		return "", 0, fmt.Errorf("registry token: %w", err)
	}
	ttl := registryTokenFallbackTTL
	if tok.ExpiresIn > 10*time.Second {
		ttl = tok.ExpiresIn - 5*time.Second
	}
	return "Bearer " + tok.AccessToken, ttl, nil
}

type releasingBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *releasingBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func drain(resp *http.Response) {
	_, _ = io.CopyN(io.Discard, resp.Body, 64<<10)
	_ = resp.Body.Close()
}

// RegistryFetch implements secretdpb.SecretdServer (ADR-20261110210000
// slice 3j).
func (s *Server) RegistryFetch(req *secretdpb.RegistryFetchRequest, stream grpc.ServerStreamingServer[secretdpb.RegistryFetchResponse]) error {
	ctx := stream.Context()
	subject := registrySubject{appID: req.GetAppId(), jobID: req.GetJobId()}
	method := req.GetMethod()
	if (subject.appID == "") == (subject.jobID == "") || (method != http.MethodGet && method != http.MethodHead) {
		return rpcErr(codes.InvalidArgument, "secretd needs an app id or a job id, and a GET or HEAD")
	}
	target, err := parseRegistryTarget(req.GetUrl())
	if err != nil {
		return rpcErr(codes.InvalidArgument, "secretd refused the registry url: "+err.Error())
	}
	if len(s.buildIdentities) == 0 {
		return rpcErr(codes.FailedPrecondition, "secretd holds no build identity")
	}
	var name, nodeID, via, username string
	var sealed []byte
	if subject.jobID != "" {
		if name, nodeID, via, err = s.authorizeJobRegistryFetch(ctx, subject.jobID); err != nil {
			return err
		}
		username, sealed, err = s.store.JobRegistryCredential(ctx, subject.jobID, target.url.Host)
	} else {
		if name, nodeID, via, err = s.authorizeRegistryFetch(ctx, subject.appID); err != nil {
			return err
		}
		username, sealed, err = s.store.RegistryCredential(ctx, subject.appID, target.url.Host)
	}
	if errors.Is(err, ErrUnknownRegistryCredential) {
		return rpcErr(codes.NotFound, "secretd found no credential for that registry")
	}
	if err != nil {
		s.log.Error("secretd: registry credential lookup", "subject", logsanitize.Field(subject.key()), "err", err)
		return rpcErr(codes.Unavailable, "secretd could not look up the registry credential")
	}
	namespace, password, err := secretbox.OpenBytesMulti(s.buildIdentities, sealed)
	if err == nil && namespace != RegistryCredentialNamespace {
		err = fmt.Errorf("namespace %q, want %s", namespace, RegistryCredentialNamespace)
	}
	if err != nil {
		clear(password)
		s.log.Warn("secretd: registry password does not open", "subject", logsanitize.Field(subject.key()),
			"registry", logsanitize.Field(target.url.Host), "err", err)
		s.metrics.refuse(rpcRegistryFetch, ReasonEnvelope)
		return rpcErr(codes.FailedPrecondition, "secretd could not open the registry credential")
	}
	auth, err := s.registry.creds.RegistryCredential(ctx, target.url.Host, &oci.BasicAuth{Username: username, Password: string(password)})
	clear(password)
	if err != nil {
		s.log.Warn("secretd: registry credential exchange", "subject", logsanitize.Field(subject.key()),
			"registry", logsanitize.Field(target.url.Host), "err", err)
		return rpcErr(codes.Unavailable, "secretd could not exchange the registry credential")
	}
	// A pull reads one manifest and many blobs; the manifest read is the
	// open the audit records.
	if target.manifest {
		record := func() error { return s.store.RecordOpen(ctx, nodeID, subject.appID, via, 1) }
		if subject.jobID != "" {
			record = func() error { return s.store.RecordJobOpen(ctx, nodeID, subject.jobID, via) }
		}
		if err := record(); err != nil {
			s.log.Error("secretd: record open", "node", logsanitize.Field(name), "subject", logsanitize.Field(subject.key()), "err", err)
			return rpcErr(codes.Unavailable, "secretd could not record the open")
		}
	}
	resp, err := s.registry.fetch(ctx, subject.key(), method, target, req.GetHeaders(), auth)
	if err != nil {
		// A url.Error names the URL, which after a redirect is a
		// presigned storage URL: log the cause alone.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		s.log.Warn("secretd: registry fetch", "subject", logsanitize.Field(subject.key()),
			"registry", logsanitize.Field(target.url.Host), "err", logsanitize.Field(err.Error()))
		return rpcErr(codes.Unavailable, "secretd could not reach the registry")
	}
	defer func() { _ = resp.Body.Close() }()
	s.metrics.open(rpcRegistryFetch, via)
	return streamRegistryResponse(stream, resp)
}

// registrySubject is whose credential a fetch uses: an app's, or a job's
// (jobs belong to an account, not an app).
type registrySubject struct{ appID, jobID string }

// key names the subject in caches, budgets and logs.
func (r registrySubject) key() string {
	if r.jobID != "" {
		return "job:" + r.jobID
	}
	return r.appID
}

// authorizeJobRegistryFetch admits the node that holds the job's live image
// materialization lease (jobs.image_materialization_lease_owner is
// "<node name>/<claim>"), within its distinct-subject budget (slice 3j-2b).
func (s *Server) authorizeJobRegistryFetch(ctx context.Context, jobID string) (name, nodeID, via string, err error) {
	name, nodeID, err = s.caller(ctx, rpcRegistryFetch)
	if err != nil {
		return "", "", "", err
	}
	owner, until, err := s.store.JobMaterializationLease(ctx, jobID)
	if errors.Is(err, ErrUnknownJob) {
		return "", "", "", rpcErr(codes.NotFound, "secretd found no such job")
	}
	if err != nil {
		s.log.Error("secretd: job lease lookup", "job_id", logsanitize.Field(jobID), "err", err)
		return "", "", "", rpcErr(codes.Unavailable, "secretd could not look up the job")
	}
	if !strings.HasPrefix(owner, name+"/") || !s.now().Before(until) {
		s.log.Warn("secretd: refused job registry fetch", "node", logsanitize.Field(name), "job_id", logsanitize.Field(jobID))
		s.metrics.refuse(rpcRegistryFetch, ReasonNotAuthorized)
		return "", "", "", rpcErr(codes.PermissionDenied, "secretd refused: the caller does not hold the job's materialization lease")
	}
	if !s.budget.allow(nodeID, "job:"+jobID, s.now()) {
		s.log.Error("secretd: node over its distinct-app budget", "node", logsanitize.Field(name), "job_id", logsanitize.Field(jobID))
		s.metrics.refuse(rpcRegistryFetch, ReasonBudgetExhausted)
		return "", "", "", rpcErr(codes.ResourceExhausted, "secretd refused: the caller opened too many distinct apps this hour")
	}
	return name, nodeID, "grant", nil
}

// imagePreparationWindow bounds how old a preparation may be and still let
// its node pull: a preparation that stopped updating is not a pull.
const imagePreparationWindow = 6 * time.Hour

// authorizeRegistryFetch admits the app's owner, a granted peer, or the node
// that holds an unfinished image preparation of one of the app's
// deployments: the scheduler names the node that prepares a deployment's
// image, which is not always the owner. The password never leaves secretd
// either way; the node gets the image it was asked to prepare.
func (s *Server) authorizeRegistryFetch(ctx context.Context, appID string) (name, nodeID, via string, err error) {
	name, nodeID, err = s.caller(ctx, rpcRegistryFetch)
	if err != nil {
		return "", "", "", err
	}
	target, err := s.store.AppTarget(ctx, appID)
	if err != nil {
		return "", "", "", rpcErr(codes.NotFound, "secretd found no such app")
	}
	via, err = s.authorize(ctx, appID, nodeID, target)
	if err != nil {
		preparing, perr := s.store.PreparesImage(ctx, appID, name, s.now().Add(-imagePreparationWindow))
		if perr != nil {
			s.log.Error("secretd: image preparation lookup", "app_id", logsanitize.Field(appID), "err", perr)
			return "", "", "", rpcErr(codes.Unavailable, "secretd could not look up the image preparation")
		}
		if !preparing {
			s.log.Warn("secretd: refused registry fetch", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID), "err", err)
			s.metrics.refuse(rpcRegistryFetch, ReasonNotAuthorized)
			return "", "", "", rpcErr(codes.PermissionDenied, "secretd refused: the caller neither owns the app, holds a grant, nor prepares its image")
		}
		// secret_open_audit.via is owner or grant: a preparation is
		// recorded as a grant the scheduler gave.
		via = "grant"
	}
	if !s.budget.allow(nodeID, appID, s.now()) {
		s.log.Error("secretd: node over its distinct-app budget", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID))
		s.metrics.refuse(rpcRegistryFetch, ReasonBudgetExhausted)
		return "", "", "", rpcErr(codes.ResourceExhausted, "secretd refused: the caller opened too many distinct apps this hour")
	}
	return name, nodeID, via, nil
}

func streamRegistryResponse(stream grpc.ServerStreamingServer[secretdpb.RegistryFetchResponse], resp *http.Response) error {
	head := &secretdpb.RegistryFetchHead{Status: int32(resp.StatusCode)}
	for _, name := range registryReturnHeaders {
		for _, v := range resp.Header.Values(name) {
			head.Headers = append(head.Headers, &secretdpb.RegistryHeader{Name: name, Value: v})
		}
	}
	if err := stream.Send(&secretdpb.RegistryFetchResponse{Frame: &secretdpb.RegistryFetchResponse_Head{Head: head}}); err != nil {
		return err
	}
	buf := make([]byte, registryChunkBytes)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if serr := stream.Send(&secretdpb.RegistryFetchResponse{Frame: &secretdpb.RegistryFetchResponse_BodyChunk{BodyChunk: chunk}}); serr != nil {
				return serr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return rpcErr(codes.Unavailable, "registry body: "+err.Error())
		}
	}
}
