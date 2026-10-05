package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
)

type fieldOwnershipRequest struct {
	App         string   `json:"app,omitempty"`
	Project     string   `json:"project,omitempty"`
	Environment string   `json:"environment"`
	Paths       []string `json:"paths"`
	Manager     string   `json:"manager"`
}

func (c *client) fieldOwnership(ctx context.Context, in fieldOwnershipRequest, release bool) error {
	if len(in.Paths) == 0 {
		return nil
	}
	in.Manager = "terraform"
	method := http.MethodPut
	if release {
		method = http.MethodDelete
	}
	return c.request(ctx, method, "/v1/environment-field-ownership", in, nil, true)
}
func (c *client) envOwnership(ctx context.Context, app, scope, key string, release bool) error {
	if scope == "" {
		scope = defaultEnvScope
	}
	return c.fieldOwnership(ctx, fieldOwnershipRequest{App: app, Environment: scope, Paths: []string{"variables/" + key}}, release)
}
func configOwnershipPaths(values json.RawMessage) []string {
	var object map[string]json.RawMessage
	_ = json.Unmarshal(values, &object)
	paths := make([]string, 0, len(object))
	for key := range object {
		paths = append(paths, "configuration/"+key)
	}
	sort.Strings(paths)
	return paths
}
func (c *client) configOwnership(ctx context.Context, project, environment string, values json.RawMessage, release bool) error {
	return c.fieldOwnership(ctx, fieldOwnershipRequest{Project: project, Environment: environment, Paths: configOwnershipPaths(values)}, release)
}

// Claim the complete write set, including removed keys, before replacing a
// configuration. Failed writes keep the reservation for a safe retry.
func (c *client) updateOwnedConfig(ctx context.Context, project, environment string, values json.RawMessage) (projectEnvironmentConfigResponse, error) {
	old, err := c.getProjectEnvironmentConfig(ctx, project, environment)
	if err != nil && !isNotFound(err) {
		return projectEnvironmentConfigResponse{}, err
	}
	var all, next map[string]json.RawMessage
	_ = json.Unmarshal(old.Values, &all)
	if all == nil {
		all = map[string]json.RawMessage{}
	}
	_ = json.Unmarshal(values, &next)
	for key, value := range next {
		all[key] = value
	}
	claimed, _ := json.Marshal(all)
	if err = c.configOwnership(ctx, project, environment, claimed, false); err != nil {
		return projectEnvironmentConfigResponse{}, err
	}
	out, err := c.updateProjectEnvironmentConfig(ctx, project, environment, projectEnvironmentConfigRequest{Values: values})
	if err != nil {
		return out, err
	}
	if err = c.releaseRemovedConfigOwnership(ctx, project, environment, old.Values, values); err != nil {
		return out, err
	}
	return out, nil
}

func (c *client) deploymentOwnership(ctx context.Context, app, scope string, release bool) error {
	if scope == "" {
		scope = defaultEnvScope
	}
	return c.fieldOwnership(ctx, fieldOwnershipRequest{App: app, Environment: scope, Paths: []string{"source"}}, release)
}

func (c *client) releaseRemovedConfigOwnership(ctx context.Context, project, environment string, previous, current json.RawMessage) error {
	var removed, next map[string]json.RawMessage
	_ = json.Unmarshal(previous, &removed)
	_ = json.Unmarshal(current, &next)
	for key := range next {
		delete(removed, key)
	}
	raw, _ := json.Marshal(removed)
	return c.configOwnership(ctx, project, environment, raw, true)
}
