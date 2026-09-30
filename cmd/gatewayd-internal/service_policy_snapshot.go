// adr: 375
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type servicePolicyApps interface {
	AppByID(context.Context, string) (state.App, error)
	AppBySlug(context.Context, string) (state.App, error)
	PreviewAppByProjectWorkload(context.Context, string, string, int, string) (state.App, error)
	ScenarioTestMemberByApp(context.Context, string) (state.ScenarioTestMember, error)
	ScenarioTestAppByWorkload(context.Context, string, string, string) (state.App, error)
}

type serviceProjectPolicyReader interface {
	GetGitHubDeployPolicy(context.Context, string, string) (state.GitHubDeployPolicy, error)
}

// Discovery, alias access and authorization use one committed read view. The
// transaction is released before the gateway queues, wakes or dispatches.
func newServicePolicyPinner(store state.ServicePolicySnapshotStore) gateway.ServicePolicyPinner {
	return func(ctx context.Context, caller, service string, alias bool) (gateway.ServicePolicySnapshot, error) {
		var result gateway.ServicePolicySnapshot
		if store == nil || !isAppID(caller) {
			return result, gateway.ErrServiceProxyUnavailable
		}
		err := store.WithServicePolicySnapshot(ctx, func(source state.ServicePolicyReader) error {
			reader := &servicePolicyEvidenceReader{ServicePolicyReader: source, apps: make(map[string]servicePolicyAppInput)}
			var err error
			result.AliasAllowed, err = newServiceAliasAllowed(reader)(ctx, caller, service)
			if err != nil {
				return err
			}
			if !alias || result.AliasAllowed {
				result.Target, result.Found, err = newServiceProxyResolver(reader)(ctx, caller, service)
				if err != nil {
					return err
				}
				if result.Found {
					result.Caller, result.AuthorizationError = newServiceProxyAuthorizer(reader)(ctx, caller, result.Target.AppID)
					if err := result.AuthorizationError; err != nil && !isServicePolicyDenial(err) {
						return err
					}
					if result.AuthorizationError == nil {
						result.Routing, err = loadServiceRoutingSnapshot(ctx, source, result)
						if err != nil {
							return err
						}
					}
				}
			}
			encoded, err := json.Marshal(reader)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(encoded)
			result.InputRevision = "service-inputs-v1:" + hex.EncodeToString(digest[:])
			return nil
		})
		return result, err
	}
}

func isServicePolicyDenial(err error) bool {
	return errors.Is(err, gateway.ErrServiceProxyDenied) || errors.Is(err, gateway.ErrServiceProxyBindingDenied) ||
		errors.Is(err, gateway.ErrServiceProxyCallerDenied) || errors.Is(err, gateway.ErrServiceProxyPreviewDenied) ||
		errors.Is(err, gateway.ErrServiceProxyPreviewProductionDenied)
}

// Keep only relevant customer intent in the fingerprint, never credentials,
// build settings or unrelated manifest content.
type servicePolicyAppInput struct {
	ID, AccountID, Slug, ProjectID, PreviewOfSlug, PreviewPrState, AppProtocol string
	Status                                                                     state.AppStatus
	PreviewPrNumber                                                            int
	PreviewExpiresAt                                                           *time.Time
	WebSocketEnabled                                                           bool
	Manifest                                                                   state.AppManifest
}

type servicePolicyEvidenceReader struct {
	state.ServicePolicyReader `json:"-"`
	apps                      map[string]servicePolicyAppInput
	loaded                    map[string]state.App
	Members                   map[string]state.ScenarioTestMember
	Projects                  map[string]state.PreviewServicePolicy
}

func (s *servicePolicyEvidenceReader) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Apps     map[string]servicePolicyAppInput
		Members  map[string]state.ScenarioTestMember
		Projects map[string]state.PreviewServicePolicy
	}{s.apps, s.Members, s.Projects})
}

func (s *servicePolicyEvidenceReader) recordApp(app state.App, err error) (state.App, error) {
	if err != nil {
		return app, err
	}
	m := app.Manifest
	input := servicePolicyAppInput{ID: app.ID, AccountID: app.AccountID, Slug: app.Slug, ProjectID: app.ProjectID,
		PreviewOfSlug: app.PreviewOfSlug, PreviewPrState: app.PreviewPrState, AppProtocol: app.AppProtocol,
		Status: app.Status, PreviewPrNumber: app.PreviewPrNumber, PreviewExpiresAt: app.PreviewExpiresAt, WebSocketEnabled: app.WebSocketEnabled,
		Manifest: state.AppManifest{ServiceBindings: m.ServiceBindings, ServiceReliability: m.ServiceReliability,
			ServiceBindingPolicy: m.ServiceBindingPolicy, ServiceBindingTransport: m.ServiceBindingTransport,
			PreviewServiceCallsPolicy: m.PreviewServiceCallsPolicy, AllowedServiceCallers: m.AllowedServiceCallers, AllowedServiceCallScopes: m.AllowedServiceCallScopes}}
	encoded, cloneErr := json.Marshal(input)
	if cloneErr != nil {
		return state.App{}, cloneErr
	}
	if cloneErr := json.Unmarshal(encoded, &input); cloneErr != nil {
		return state.App{}, cloneErr
	}
	s.apps[app.ID] = input
	app.Manifest = input.Manifest
	if s.loaded == nil {
		s.loaded = make(map[string]state.App)
	}
	s.loaded[app.ID] = app
	return app, nil
}

func (s *servicePolicyEvidenceReader) AppByID(ctx context.Context, id string) (state.App, error) {
	if app, ok := s.loaded[id]; ok {
		return app, nil
	}
	return s.recordApp(s.ServicePolicyReader.AppByID(ctx, id))
}
func (s *servicePolicyEvidenceReader) AppBySlug(ctx context.Context, slug string) (state.App, error) {
	return s.recordApp(s.ServicePolicyReader.AppBySlug(ctx, slug))
}
func (s *servicePolicyEvidenceReader) PreviewAppByProjectWorkload(ctx context.Context, account, project string, pr int, workload string) (state.App, error) {
	return s.recordApp(s.ServicePolicyReader.PreviewAppByProjectWorkload(ctx, account, project, pr, workload))
}
func (s *servicePolicyEvidenceReader) ScenarioTestAppByWorkload(ctx context.Context, account, run, workload string) (state.App, error) {
	return s.recordApp(s.ServicePolicyReader.ScenarioTestAppByWorkload(ctx, account, run, workload))
}
func (s *servicePolicyEvidenceReader) ScenarioTestMemberByApp(ctx context.Context, appID string) (state.ScenarioTestMember, error) {
	member, err := s.ServicePolicyReader.ScenarioTestMemberByApp(ctx, appID)
	if err == nil {
		if s.Members == nil {
			s.Members = make(map[string]state.ScenarioTestMember)
		}
		s.Members[appID] = member
	}
	return member, err
}
func (s *servicePolicyEvidenceReader) GetGitHubDeployPolicy(ctx context.Context, project, account string) (state.GitHubDeployPolicy, error) {
	policy, err := s.ServicePolicyReader.GetGitHubDeployPolicy(ctx, project, account)
	if err == nil {
		if s.Projects == nil {
			s.Projects = make(map[string]state.PreviewServicePolicy)
		}
		s.Projects[project] = policy.PreviewServicePolicy
	}
	return policy, err
}
