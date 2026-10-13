package postgresfixture

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
)

const remoteTokenEnv = "GREGALE_POSTGRES_FIXTURE_TOKEN"

type remoteRequest struct {
	Method     string
	Provision  mp.ProvisionRequest
	Discovery  mp.ResourceDiscoveryRequest
	Credential mp.CredentialRequest
	Delete     mp.DeleteRequest
	Resource   string
	Window     mp.UsageWindow
}

type remoteResponse struct {
	Error string
	Data  json.RawMessage
}

var remoteErrors = map[string]error{
	"not_found": mp.ErrNotFound, "conflict": mp.ErrConflict,
	"unsupported": mp.ErrUnsupported, "invalid": mp.ErrInvalid,
	"unavailable": mp.ErrUnavailable,
}

// The control server is local test infrastructure. SQL still runs over native
// TLS with independent roles; only provider management crosses this seam.
func serveProvider(t *testing.T, provider mp.Provider) (string, string) {
	t.Helper()
	token := uuid.NewString()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var request remoteRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		value, err := dispatchProvider(r.Context(), provider, request)
		response := remoteResponse{}
		if err != nil {
			response.Error = "unavailable"
			for name, candidate := range remoteErrors {
				if errors.Is(err, candidate) {
					response.Error = name
					break
				}
			}
		} else {
			response.Data, err = json.Marshal(value)
		}
		if err != nil && response.Error == "" {
			response.Error = "unavailable"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	return server.URL, token
}

func dispatchProvider(ctx context.Context, p mp.Provider, r remoteRequest) (any, error) {
	switch r.Method {
	case "capabilities":
		return p.Capabilities(), nil
	case "provision":
		return p.Provision(ctx, r.Provision)
	case "inspect":
		return p.Inspect(ctx, r.Resource)
	case "discover":
		if discovery, ok := p.(mp.ResourceDiscoverer); ok {
			return discovery.Discover(ctx, r.Discovery)
		}
		return nil, mp.ErrUnsupported
	case "delete":
		return p.Delete(ctx, r.Delete)
	case "issue":
		return p.IssueCredentials(ctx, r.Credential)
	case "revoke":
		return nil, p.RevokeCredentials(ctx, r.Credential)
	case "usage":
		return p.Usage(ctx, r.Resource, r.Window)
	default:
		return nil, mp.ErrUnsupported
	}
}

type remoteProvider struct {
	url, token   string
	client       *http.Client
	capabilities mp.Capabilities
}

// NewRemote is registered only in apid compiled with metal AND
// managed_postgres_native. No configurable upstream is added to production.
func NewRemote(config mp.BackendConfig, getenv func(string) string) (mp.Provider, error) {
	endpoint, err := url.Parse(config.Settings["control_url"])
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "" || len(config.Settings) != 1 || len(config.SecretEnv) != 1 {
		return nil, mp.ErrInvalid
	}
	ip, err := netip.ParseAddr(endpoint.Hostname())
	if err != nil || !ip.IsLoopback() || endpoint.Port() == "" || getenv == nil {
		return nil, mp.ErrInvalid
	}
	token := getenv(config.SecretEnv["control-token"])
	if token == "" {
		return nil, mp.ErrInvalid
	}
	provider := &remoteProvider{url: endpoint.String(), token: token, client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if err := provider.call(context.Background(), remoteRequest{Method: "capabilities"}, &provider.capabilities); err != nil {
		return nil, err
	}
	return provider, nil
}

func (p *remoteProvider) call(ctx context.Context, input remoteRequest, result any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return mp.ErrInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return mp.ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+p.token)
	response, err := p.client.Do(request)
	if err != nil {
		return mp.ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return mp.ErrUnavailable
	}
	var value remoteResponse
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value) != nil {
		return mp.ErrUnavailable
	}
	if value.Error != "" {
		if remoteErr, ok := remoteErrors[value.Error]; ok {
			return remoteErr
		}
		return mp.ErrUnavailable
	}
	if result != nil && json.Unmarshal(value.Data, result) != nil {
		return mp.ErrUnavailable
	}
	return nil
}
func (p *remoteProvider) Capabilities() mp.Capabilities { return p.capabilities }
func (p *remoteProvider) Provision(ctx context.Context, r mp.ProvisionRequest) (v mp.ObservedDatabase, err error) {
	err = p.call(ctx, remoteRequest{Method: "provision", Provision: r}, &v)
	return
}
func (p *remoteProvider) Inspect(ctx context.Context, id string) (v mp.ObservedDatabase, err error) {
	err = p.call(ctx, remoteRequest{Method: "inspect", Resource: id}, &v)
	return
}
func (p *remoteProvider) Discover(ctx context.Context, r mp.ResourceDiscoveryRequest) (v string, err error) {
	err = p.call(ctx, remoteRequest{Method: "discover", Discovery: r}, &v)
	return
}
func (*remoteProvider) Update(context.Context, mp.UpdateRequest) (mp.ObservedDatabase, error) {
	return mp.ObservedDatabase{}, mp.ErrUnsupported
}
func (*remoteProvider) Restore(context.Context, mp.RestoreRequest) (mp.ObservedDatabase, error) {
	return mp.ObservedDatabase{}, mp.ErrUnsupported
}
func (p *remoteProvider) Delete(ctx context.Context, r mp.DeleteRequest) (v mp.DeleteResult, err error) {
	err = p.call(ctx, remoteRequest{Method: "delete", Delete: r}, &v)
	return
}
func (p *remoteProvider) IssueCredentials(ctx context.Context, r mp.CredentialRequest) (v mp.CredentialMaterial, err error) {
	err = p.call(ctx, remoteRequest{Method: "issue", Credential: r}, &v)
	return
}
func (p *remoteProvider) RevokeCredentials(ctx context.Context, r mp.CredentialRequest) error {
	return p.call(ctx, remoteRequest{Method: "revoke", Credential: r}, nil)
}
func (p *remoteProvider) Usage(ctx context.Context, id string, w mp.UsageWindow) (v mp.Usage, err error) {
	err = p.call(ctx, remoteRequest{Method: "usage", Resource: id, Window: w}, &v)
	return
}

// DaemonEnvironment preserves the real staging gate on the owned test catalog.
// These approvals apply only to this ephemeral fixture backend fingerprint.
func (f *Fixture) DaemonEnvironment(t *testing.T) []string {
	t.Helper()
	config, err := json.Marshal(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "postgres-fixture.json")
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := mp.NewRegistry(f.Config, func(string) string { return f.remoteToken }, map[string]mp.Factory{"sqlfixture": func(mp.BackendConfig, func(string) string) (mp.Provider, error) { return f.Provider, nil }})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default(f.Config.DefaultRegion)
	if err != nil {
		t.Fatal(err)
	}
	return []string{"FAAS_MANAGED_POSTGRES_CONFIG=" + path, remoteTokenEnv + "=" + f.remoteToken,
		mp.EnvironmentEnv + "=staging", mp.QualificationEnv + "=true", mp.QualificationVersionEnv + "=" + strconv.Itoa(mp.QualificationArtifactVersion),
		mp.QualificationBackendEnv + "=" + backend.ID, mp.QualificationFingerprintEnv + "=" + backend.Fingerprint,
		mp.QualificationUntilEnv + "=" + time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339)}
}
