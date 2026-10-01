// Package devbridgeacceptance exercises the deployed public edge and real
// project workloads. Its local-process servers are the developer laptops;
// frontend, production caller and inventory must be native deployed fixtures.
package devbridgeacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
)

type Config struct {
	API, Token, Project, Environment, Payments, Frontend, Inventory string
	IdleFor                                                         time.Duration
}

type Evidence struct {
	StartedAt     time.Time                       `json:"started_at"`
	CompletedAt   time.Time                       `json:"completed_at"`
	Project       string                          `json:"project"`
	Environment   string                          `json:"environment"`
	DeploymentIDs map[string]string               `json:"deployment_ids"`
	Instances     map[string][]VMInstanceEvidence `json:"instances"`
	Engine        string                          `json:"engine"`
	SourceCommit  string                          `json:"source_commit,omitempty"`
	Topology      *NativeTopology                 `json:"native_topology,omitempty"`
	SessionIDs    map[string]string               `json:"session_ids"`
	IdleSeconds   int64                           `json:"idle_seconds"`
	Passed        []string                        `json:"passed"`
}

type NativeTopology struct {
	API          string   `json:"api_url"`
	ControlPlane string   `json:"control_plane"`
	ComputeNodes []string `json:"compute_nodes"`
}

type VMInstanceEvidence struct {
	ID           string `json:"id"`
	DeploymentID string `json:"deployment_id"`
	WakeID       string `json:"wake_id"`
	State        string `json:"state"`
}

type fixtureResponse struct {
	Payments  string `json:"payments"`
	Inventory string `json:"inventory"`
}

func Run(ctx context.Context, config Config) (report Evidence, runErr error) {
	report = Evidence{StartedAt: time.Now().UTC(), Project: config.Project, Environment: config.Environment, DeploymentIDs: make(map[string]string), Instances: make(map[string][]VMInstanceEvidence), SessionIDs: make(map[string]string), Engine: "real-vm", Passed: []string{}}
	if err := validate(config); err != nil {
		return report, err
	}
	client := api.NewClient(config.API, config.Token)
	environment, err := client.GetProjectEnvironment(ctx, config.Project, config.Environment)
	if err != nil || environment.Protected || environment.Slug == "production" || environment.Slug == "default" {
		return report, errors.New("select an owned unprotected development fixture environment")
	}
	releases, err := client.GetProjectEnvironmentReleases(ctx, config.Project, config.Environment)
	if err != nil {
		return report, errors.New("read development fixture releases")
	}
	for _, slug := range []string{config.Payments, config.Frontend, config.Inventory} {
		member, err := fixtureMember(releases, slug)
		if err != nil {
			return report, err
		}
		report.DeploymentIDs[slug] = member.DeploymentID
	}
	production, err := client.GetProjectEnvironmentReleases(ctx, config.Project, "production")
	if err != nil {
		return report, errors.New("read isolated production-caller fixture")
	}
	productionFrontend, err := fixtureMember(production, config.Frontend)
	if err != nil {
		return report, err
	}
	if err := secureURL(productionFrontend.URL); err != nil {
		return report, err
	}
	report.DeploymentIDs["production/"+config.Frontend] = productionFrontend.DeploymentID
	alice, err := attach(ctx, client, config, "alice")
	if err != nil {
		return report, err
	}
	defer func() {
		runErr = errors.Join(runErr, alice.close(ctx))
		if runErr != nil {
			report.CompletedAt = time.Time{}
		}
	}()
	report.SessionIDs["alice"] = alice.session.Session.ID
	bob, err := attach(ctx, client, config, "bob")
	if err != nil {
		return report, err
	}
	defer func() {
		runErr = errors.Join(runErr, bob.close(ctx))
		if runErr != nil {
			report.CompletedAt = time.Time{}
		}
	}()
	report.SessionIDs["bob"] = bob.session.Session.ID
	ordinary := alice.session.EnvironmentURL + "/charge"
	if err := assertResponse(ctx, config, ordinary, nil, "remote"); err != nil {
		return report, fmt.Errorf("ordinary routing: %w", err)
	}
	report.Passed = append(report.Passed, "ordinary_remote_routing")
	for _, laptop := range []*laptop{alice, bob} {
		if err := assertResponse(ctx, config, ordinary, laptop.headers(), laptop.name); err != nil {
			return report, fmt.Errorf("developer isolation: %w", err)
		}
	}
	report.Passed = append(report.Passed, "real_frontend_local_payments_remote_inventory", "two_developer_isolation")
	before := alice.requests.Load()
	// This fixture-only header is captured INSIDE the production frontend,
	// so denial must happen on its real service-proxy call, not public ingress.
	probe := http.Header{"X-Gregale-Bridge-Acceptance-Context": {alice.requestContext()}}
	status, body, err := request(ctx, config, productionFrontend.URL+"/charge", probe)
	var denial struct {
		Caller         string `json:"caller"`
		UpstreamStatus int    `json:"upstream_status"`
	}
	if err != nil || status != http.StatusForbidden || json.Unmarshal(body, &denial) != nil || denial.Caller != "frontend" || denial.UpstreamStatus != http.StatusForbidden || alice.requests.Load() != before {
		return report, errors.New("production VM caller was not denied at its service hop")
	}
	report.Passed = append(report.Passed, "production_vm_caller_denied")
	timer := time.NewTimer(config.IdleFor)
	select {
	case <-ctx.Done():
		timer.Stop()
		return report, ctx.Err()
	case <-timer.C:
	}
	report.IdleSeconds = int64(config.IdleFor.Seconds())
	activity, err := client.GetDevBridgeActivity(ctx, alice.session.Session.ID)
	if err != nil || activity.ConnectionState != "connected" {
		return report, errors.New("idle edge connection did not survive the requested interval")
	}
	if err := assertResponse(ctx, config, ordinary, alice.headers(), "alice"); err != nil {
		return report, err
	}
	report.Passed = append(report.Passed, "idle_public_edge_lifetime")
	if err := alice.reconnect(ctx); err != nil {
		return report, err
	}
	if err := assertResponse(ctx, config, ordinary, alice.headers(), "alice"); err != nil {
		return report, err
	}
	report.Passed = append(report.Passed, "connection_replacement")
	if err := client.RevokeDevBridge(ctx, alice.session.Session.ID); err != nil {
		return report, errors.New("revoke development session")
	}
	status, _, err = request(ctx, config, ordinary, alice.headers())
	if err != nil || status != 403 {
		return report, errors.New("revoked session remained routable")
	}
	if err := assertResponse(ctx, config, ordinary, bob.headers(), "bob"); err != nil {
		return report, err
	}
	if err := assertResponse(ctx, config, ordinary, nil, "remote"); err != nil {
		return report, err
	}
	report.Passed = append(report.Passed, "revocation", "teammate_and_ordinary_traffic_preserved")
	for key, deployment := range report.DeploymentIDs {
		slug := strings.TrimPrefix(key, "production/")
		instances, err := client.ListInstancesWithHistory(ctx, slug, true)
		if err != nil {
			return report, fmt.Errorf("read fixture VM evidence for %s", key)
		}
		for _, instance := range instances {
			if instance.DeploymentID == deployment && instance.WakeID != "" && (instance.State == "RUNNING" || instance.State == "PARKED") {
				report.Instances[key] = append(report.Instances[key], VMInstanceEvidence{ID: instance.ID, DeploymentID: deployment, WakeID: instance.WakeID, State: instance.State})
			}
		}
		if len(report.Instances[key]) == 0 {
			return report, fmt.Errorf("fixture %s has no running/parked VM wake evidence for its deployed revision", key)
		}
	}
	report.Passed = append(report.Passed, "deployed_revision_vm_wake_evidence")
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func validate(c Config) error {
	if err := secureURL(c.API); err != nil {
		return err
	}
	if c.Token == "" || c.Project == "" || c.Environment == "" || c.Payments == "" || c.Frontend == "" || c.Inventory == "" {
		return errors.New("native bridge fixture configuration is incomplete")
	}
	if c.IdleFor < time.Second || c.IdleFor >= api.DevBridgeSessionTTL-5*time.Minute {
		return errors.New("idle interval must fit within the one-hour lease with cleanup margin")
	}
	return nil
}

func secureURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("native acceptance requires HTTPS URLs without embedded credentials or query strings")
	}
	return nil
}

func fixtureMember(releases api.ProjectEnvironmentReleaseListResponse, slug string) (api.ProjectEnvironmentReleaseWorkloadResponse, error) {
	for _, member := range releases.Workloads {
		if member.WorkloadSlug == slug || member.WorkloadName == slug {
			if member.Status != "live" || member.DeploymentID == "" || member.URL == "" {
				return member, fmt.Errorf("fixture %s must have a live environment revision", slug)
			}
			return member, nil
		}
	}
	return api.ProjectEnvironmentReleaseWorkloadResponse{}, fmt.Errorf("fixture %s is missing from environment releases", slug)
}

func request(ctx context.Context, c Config, endpoint string, headers http.Header) (int, []byte, error) {
	if err := secureURL(endpoint); err != nil {
		return 0, nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	for key, values := range headers {
		r.Header[key] = append([]string(nil), values...)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return 0, nil, errors.New("public fixture request failed")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, api.DevBridgeReplayResponseBytes))
	return response.StatusCode, body, err
}

func assertResponse(ctx context.Context, c Config, endpoint string, headers http.Header, developer string) error {
	status, body, err := request(ctx, c, endpoint, headers)
	var response fixtureResponse
	if err != nil || status != 200 || json.Unmarshal(body, &response) != nil || response.Payments != developer || response.Inventory != "remote" {
		return errors.New("fixture response did not traverse the expected payments/inventory graph")
	}
	return nil
}

type laptop struct {
	name     string
	client   *api.Client
	session  api.CreateDevBridgeResponse
	local    *httptest.Server
	socket   *websocket.Conn
	requests atomic.Int64
}

func attach(ctx context.Context, client *api.Client, config Config, name string) (*laptop, error) {
	session, err := client.CreateDevBridge(ctx, api.CreateDevBridgeRequest{App: config.Payments, Environment: config.Environment, DeveloperID: "native-acceptance-" + name, Entrypoint: config.Frontend})
	if err != nil {
		return nil, errors.New("create bridge session; check native deployment enablement and account capacity")
	}
	p := &laptop{name: name, client: client, session: session}
	if err := secureURL(session.EnvironmentURL); err != nil {
		_ = p.close(ctx)
		return nil, err
	}
	inventoryApp, err := client.GetApp(ctx, config.Inventory)
	if err != nil {
		_ = p.close(ctx)
		return nil, errors.New("resolve the owned inventory fixture app")
	}
	var inventory string
	for _, dependency := range session.Dependencies {
		if dependency.AppID == inventoryApp.ID {
			inventory = dependency.AppID
		}
	}
	if inventory == "" {
		_ = p.close(ctx)
		return nil, errors.New("payments fixture must declare its inventory service binding")
	}
	p.local = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		endpoint := strings.TrimRight(config.API, "/") + "/v1/dev/bridges/" + session.Session.ID + "/dependencies/" + inventory + "/stock"
		request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
		if err != nil {
			http.Error(w, "dependency request invalid", http.StatusBadGateway)
			return
		}
		request.Header = http.Header{devbridge.AccountHeader: {session.Session.Scope.AccountID}, devbridge.TokenHeader: {session.Credentials.AttachmentToken}}
		httpClient := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := httpClient.Do(request)
		if err != nil {
			http.Error(w, "dependency unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = response.Body.Close() }()
		var value struct{ Inventory string }
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, api.DevBridgeReplayResponseBytes)).Decode(&value) != nil || value.Inventory != "remote" {
			http.Error(w, "dependency response invalid", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixtureResponse{Payments: name, Inventory: value.Inventory})
	}))
	if err := p.reconnect(ctx); err != nil {
		_ = p.close(ctx)
		return nil, err
	}
	return p, nil
}

func (p *laptop) reconnect(ctx context.Context) error {
	endpoint := "wss" + strings.TrimPrefix(p.client.BaseURL(), "https") + "/v1/dev/bridges/" + p.session.Session.ID + "/connect"
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Subprotocols: []string{"gregale-dev-bridge-v1"}}
	socket, response, err := dialer.DialContext(ctx, endpoint, http.Header{devbridge.AccountHeader: {p.session.Session.Scope.AccountID}, devbridge.TokenHeader: {p.session.Credentials.AttachmentToken}})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return errors.New("laptop attachment through public TLS edge failed")
	}
	previous := p.socket
	p.socket = socket
	if previous != nil {
		_ = previous.Close()
	}
	target, _ := url.Parse(p.local.URL)
	go func() {
		_ = devbridge.ServeLocal(ctx, devbridge.NewWebSocketConn(socket), target, api.DevBridgeMaxConcurrentRequests)
	}()
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		activity, err := p.client.GetDevBridgeActivity(ready, p.session.Session.ID)
		if err == nil && activity.ConnectionState == "connected" {
			endpoint := strings.TrimRight(p.client.BaseURL(), "/") + "/v1/dev/bridges/" + p.session.Session.ID + "/status"
			r, err := http.NewRequestWithContext(ready, http.MethodGet, endpoint, nil)
			if err != nil {
				return err
			}
			r.Header = http.Header{devbridge.AccountHeader: {p.session.Session.Scope.AccountID}, devbridge.TokenHeader: {p.session.Credentials.AttachmentToken}}
			client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			response, err := client.Do(r)
			if err == nil {
				var status struct {
					Connected bool `json:"connected"`
				}
				decoded := json.NewDecoder(io.LimitReader(response.Body, api.DevBridgeReplayResponseBytes)).Decode(&status)
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK && decoded == nil && status.Connected {
					return nil
				}
			}
		}
		select {
		case <-ready.Done():
			return errors.New("laptop connection was not observed by API")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (p *laptop) requestContext() string {
	return (devbridge.RequestContext{AccountID: p.session.Session.Scope.AccountID, SessionID: p.session.Session.ID, Token: p.session.Credentials.RequestToken}).Encode()
}
func (p *laptop) headers() http.Header {
	return http.Header{devbridge.ContextHeader: {p.requestContext()}}
}
func (p *laptop) close(parent context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	err := p.client.RevokeDevBridge(ctx, p.session.Session.ID)
	if p.socket != nil {
		_ = p.socket.Close()
	}
	if p.local != nil {
		p.local.Close()
	}
	if err != nil {
		return errors.New("native acceptance session cleanup failed; revoke the acceptance developer sessions")
	}
	return nil
}
