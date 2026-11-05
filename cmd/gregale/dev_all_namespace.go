package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// The `gregale dev --all` supervisor hands each child loop its group
// (ADR-20261025121530, ADR-20261026105459): the workspace every app's
// developer environment lives in, the service name the child registers under,
// and the group's services it declares it calls. They are internal to the
// supervisor and its children.
const (
	devGroupWorkspaceEnv = "GREGALE_DEV_GROUP_WORKSPACE"
	devGroupServiceEnv   = "GREGALE_DEV_GROUP_SERVICE"
	devGroupDependsEnv   = "GREGALE_DEV_GROUP_DEPENDS"
)

// devGroupWorkspace returns the supervisor's group workspace when this
// process is a `dev --all` child, or "".
func devGroupWorkspace() string {
	workspace := os.Getenv(devGroupWorkspaceEnv)
	if len(workspace) != 32 {
		return ""
	}
	for _, ch := range workspace {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return ""
		}
	}
	return workspace
}

// devGroupService returns the service name this child registers under, or
// project when the supervisor named none.
func devGroupService(project string) string {
	if service := strings.TrimSpace(os.Getenv(devGroupServiceEnv)); api.ValidServiceName(service) {
		return service
	}
	return project
}

// devGroupDepends returns the group's services this child declares it calls.
func devGroupDepends() []string {
	var out []string
	for _, service := range strings.Split(os.Getenv(devGroupDependsEnv), ",") {
		if service = strings.TrimSpace(service); api.ValidServiceName(service) {
			out = append(out, service)
		}
	}
	return out
}

// devGroupClient is the API surface joining a `dev --all` group needs.
type devGroupClient interface {
	RegisterScenarioTest(context.Context, string, api.RegisterScenarioTestRequest) error
}

// joinDevGroup registers a `dev --all` child's developer environment in its
// group's namespace under service once the environment exists. A call from
// one member to another's name (`http://api.internal:10081`,
// `api.svc.gregale`) then reaches that member's developer environment, never
// production. An API that predates service names (ADR-20261026105459)
// refuses a name that is not also an app slug, such as `ui`; the app then
// joins under project, which is always one.
func joinDevGroup(ctx context.Context, client devGroupClient, workspace, service, project, slug string, warn io.Writer) error {
	err := registerDevGroupMember(ctx, client, workspace, service, slug)
	var apiErr *api.APIError
	if service != project && errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusBadRequest {
		PrintWarn(warn, "this Gregale API cannot register the service name %q yet; the other apps call this one %q", service, project)
		err = registerDevGroupMember(ctx, client, workspace, project, slug)
	}
	return err
}

func registerDevGroupMember(ctx context.Context, client devGroupClient, workspace, name, slug string) error {
	err := client.RegisterScenarioTest(ctx, workspace, api.RegisterScenarioTestRequest{
		Members: []api.ScenarioTestWorkload{{Workload: name, AppSlug: slug}},
	})
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusConflict {
		return nil // registered by an earlier run of the same group
	}
	return err
}

// devGroupServiceConfig is the developer config naming each group service
// this app declares it calls: the same GREGALE_SERVICE_<NAME>_URL,
// _HTTPS_URL and _HOST values a deployed project service gets for its
// depends_on, which resolve inside the group.
func devGroupServiceConfig(depends []string) []secretsPair {
	pairs := make([]secretsPair, 0, 3*len(depends))
	for _, service := range depends {
		pairs = append(pairs,
			secretsPair{Key: api.ServiceBindingEnvKey(service), Value: api.ServiceBindingCanonicalURL(service, api.ServiceBindingTransportHTTP)},
			secretsPair{Key: api.ServiceBindingHTTPSEnvKey(service), Value: api.ServiceBindingCanonicalURL(service, api.ServiceBindingTransportHTTPS)},
			secretsPair{Key: api.ServiceBindingHostEnvKey(service), Value: service + ".svc.gregale"},
		)
	}
	return pairs
}

// devGroupSecretClient sets developer config on a developer environment.
type devGroupSecretClient interface {
	SetSecretWithScope(ctx context.Context, slug, key, value, scope string) error
}

// setDevGroupServiceConfig writes devGroupServiceConfig to the developer
// environment. A failure costs only the variables, not the names, so it is
// reported and the loop keeps running.
func setDevGroupServiceConfig(ctx context.Context, client devGroupSecretClient, slug string, depends []string, warn io.Writer) {
	for _, pair := range devGroupServiceConfig(depends) {
		if err := client.SetSecretWithScope(ctx, slug, pair.Key, pair.Value, ""); err != nil {
			PrintWarn(warn, "could not set %s; call the other apps by name instead (%v)", pair.Key, err)
			return
		}
	}
}

// describeDevGroupService explains a service name the other apps cannot
// use as declared.
func describeDevGroupService(app devAllApp) string {
	declared := strings.TrimSpace(app.declared)
	if declared == "" || api.ServiceDNSName(declared) == app.Service {
		return ""
	}
	return fmt.Sprintf("service %q is not a DNS name; the other apps call it %s", declared, app.Service)
}
