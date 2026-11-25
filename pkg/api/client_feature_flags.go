package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func flagsPath(project, environment string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/flags"
}

// ProjectFlags returns the immutable bundle and its current version.
func (c *Client) ProjectFlags(ctx context.Context, project, environment string) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, flagsPath(project, environment), nil, &out)
	return out, err
}

// PublishProjectFlags requires an expected_version and full config JSON body.
func (c *Client) PublishProjectFlags(ctx context.Context, project, environment string, body json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodPut, flagsPath(project, environment), body, &out)
	return out, err
}
func (c *Client) ProjectFlagVersions(ctx context.Context, project, environment string, before int64) (json.RawMessage, error) {
	var out json.RawMessage
	p := flagsPath(project, environment) + "/versions"
	if before > 0 {
		p += "?before_version=" + strconv.FormatInt(before, 10)
	}
	err := c.do(ctx, http.MethodGet, p, nil, &out)
	return out, err
}
func (c *Client) InspectProjectFlag(ctx context.Context, project, environment, key, customer string, version int64, fallbackVariants ...string) (json.RawMessage, error) {
	return c.InspectProjectFlagForSubject(ctx, project, environment, key, customer, "", version, fallbackVariants...)
}

// InspectProjectFlagForSubject simulates a decision for one authenticated
// application subject inside a verified platform customer.
func (c *Client) InspectProjectFlagForSubject(ctx context.Context, project, environment, key, customer, subject string, version int64, fallbackVariants ...string) (json.RawMessage, error) {
	var fallbackVariant string
	if len(fallbackVariants) > 0 {
		fallbackVariant = fallbackVariants[0]
	}
	return c.InspectProjectFlagWithContext(ctx, project, environment, key, InspectFlagContext{Customer: customer, Subject: subject, Version: version, FallbackVariant: fallbackVariant})
}

// ProjectFlagLifecycle reads the lifecycle report (ADR 20261114150000 §4).
func (c *Client) ProjectFlagLifecycle(ctx context.Context, project, environment string) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, flagsPath(project, environment)+"/lifecycle", nil, &out)
	return out, err
}

// InspectFlagContext is a simulated request for InspectProjectFlagWithContext.
type InspectFlagContext struct {
	Customer        string
	Subject         string
	Version         int64
	FallbackVariant string
	// Attributes simulates gregale.* and app.* request attributes.
	Attributes map[string]string
}

// InspectProjectFlagWithContext simulates a decision for a full request
// context, including attribute-targeted rules (ADR 20261114150000 §2).
func (c *Client) InspectProjectFlagWithContext(ctx context.Context, project, environment, key string, in InspectFlagContext) (json.RawMessage, error) {
	var out json.RawMessage
	body := map[string]any{"customer_id": in.Customer, "version": in.Version, "fallback": false, "fallback_variant": in.FallbackVariant}
	if in.Subject != "" {
		body["subject_id"] = in.Subject
	}
	if len(in.Attributes) > 0 {
		body["attributes"] = in.Attributes
	}
	err := c.do(ctx, http.MethodPost, flagsPath(project, environment)+"/"+url.PathEscape(key)+"/inspect", body, &out)
	return out, err
}
func (c *Client) RollbackProjectFlags(ctx context.Context, project, environment string, expected, version int64) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodPost, flagsPath(project, environment)+"/rollback", map[string]int64{"expected_version": expected, "version": version}, &out)
	return out, err
}
func (c *Client) ProjectFlagRequests(ctx context.Context, project, environment, key string, query url.Values) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, flagsPath(project, environment)+"/"+url.PathEscape(key)+"/requests?"+query.Encode(), nil, &out)
	return out, err
}

// ProjectFlagOutcomes returns request-weighted operational outcomes grouped by decision value.
func (c *Client) ProjectFlagOutcomes(ctx context.Context, project, environment, key string, query url.Values) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, flagsPath(project, environment)+"/"+url.PathEscape(key)+"/outcomes?"+query.Encode(), nil, &out)
	return out, err
}

// PromoteProjectFlagRollout evaluates a rule's evidence gates and publishes its next stage when healthy.
func (c *Client) PromoteProjectFlagRollout(ctx context.Context, project, environment, key string, body json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	path := flagsPath(project, environment) + "/" + url.PathEscape(key) + "/rollout/promote"
	err := c.do(ctx, http.MethodPost, path, body, &out)
	return out, err
}

// KillProjectFlag sets one flag's kill on the latest configuration
// (ADR-20261117215858). It needs no expected version and is idempotent.
func (c *Client) KillProjectFlag(ctx context.Context, project, environment, key string) (FeatureFlagKillResponse, error) {
	var out FeatureFlagKillResponse
	err := c.do(ctx, http.MethodPost, flagsPath(project, environment)+"/"+url.PathEscape(key)+"/kill", struct{}{}, &out)
	return out, err
}

// RuntimeFlags requires a Client constructed with a workload JWT for gregale:flags.
// Account API keys cannot read this endpoint.
func (c *Client) RuntimeFlags(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, http.MethodGet, "/v1/runtime/flags", nil, &out)
	return out, err
}

func experimentsPath(project, environment string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/experiments"
}

// ListProjectFlagExperiments lists an environment's experiments (ADR 20261115163113).
func (c *Client) ListProjectFlagExperiments(ctx context.Context, project, environment string) (FlagExperimentList, error) {
	var out FlagExperimentList
	err := c.do(ctx, http.MethodGet, experimentsPath(project, environment), nil, &out)
	return out, err
}

// CreateProjectFlagExperiment creates, and with req.Start starts, an experiment.
func (c *Client) CreateProjectFlagExperiment(ctx context.Context, project, environment string, req FlagExperimentCreateRequest) (FlagExperiment, error) {
	var out FlagExperiment
	err := c.do(ctx, http.MethodPost, experimentsPath(project, environment), req, &out)
	return out, err
}

// GetProjectFlagExperiment reads one experiment.
func (c *Client) GetProjectFlagExperiment(ctx context.Context, project, environment, key string) (FlagExperiment, error) {
	var out FlagExperiment
	err := c.do(ctx, http.MethodGet, experimentsPath(project, environment)+"/"+url.PathEscape(key), nil, &out)
	return out, err
}

// StartProjectFlagExperiment starts a draft experiment.
func (c *Client) StartProjectFlagExperiment(ctx context.Context, project, environment, key string) (FlagExperiment, error) {
	var out FlagExperiment
	err := c.do(ctx, http.MethodPost, experimentsPath(project, environment)+"/"+url.PathEscape(key)+"/start", nil, &out)
	return out, err
}

// StopProjectFlagExperiment stops a running experiment; its results freeze.
func (c *Client) StopProjectFlagExperiment(ctx context.Context, project, environment, key string) (FlagExperiment, error) {
	var out FlagExperiment
	err := c.do(ctx, http.MethodPost, experimentsPath(project, environment)+"/"+url.PathEscape(key)+"/stop", nil, &out)
	return out, err
}

// ShipProjectFlagExperiment stops a running experiment and publishes its
// rule serving variant to every customer it matches.
func (c *Client) ShipProjectFlagExperiment(ctx context.Context, project, environment, key, variant string) (FlagExperimentShipResponse, error) {
	var out FlagExperimentShipResponse
	err := c.do(ctx, http.MethodPost, experimentsPath(project, environment)+"/"+url.PathEscape(key)+"/ship", FlagExperimentShipRequest{Variant: variant}, &out)
	return out, err
}

// DeleteProjectFlagExperiment deletes an experiment that is not running.
func (c *Client) DeleteProjectFlagExperiment(ctx context.Context, project, environment, key string) error {
	return c.do(ctx, http.MethodDelete, experimentsPath(project, environment)+"/"+url.PathEscape(key), nil, nil)
}

// ProjectFlagExperimentResults reads an experiment's sequential results.
func (c *Client) ProjectFlagExperimentResults(ctx context.Context, project, environment, key string) (FlagExperimentResults, error) {
	var out FlagExperimentResults
	err := c.do(ctx, http.MethodGet, experimentsPath(project, environment)+"/"+url.PathEscape(key)+"/results", nil, &out)
	return out, err
}

// ProjectFlagExperimentResultsBySegment reads an experiment's results split
// by a cohort's members (ADR 20261119173702 §3).
func (c *Client) ProjectFlagExperimentResultsBySegment(ctx context.Context, project, environment, key, cohort string) (FlagExperimentResults, error) {
	var out FlagExperimentResults
	path := experimentsPath(project, environment) + "/" + url.PathEscape(key) + "/results?segment=" + url.QueryEscape(cohort)
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func experimentEnvPath(project, environment, rest string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + rest
}

// ListProjectFlagExperimentLayers lists an environment's experiment layers
// (ADR 20261119173702 §2).
func (c *Client) ListProjectFlagExperimentLayers(ctx context.Context, project, environment string) (FlagExperimentLayerList, error) {
	var out FlagExperimentLayerList
	err := c.do(ctx, http.MethodGet, experimentEnvPath(project, environment, "/experiment-layers"), nil, &out)
	return out, err
}

// CreateProjectFlagExperimentLayer creates an experiment layer.
func (c *Client) CreateProjectFlagExperimentLayer(ctx context.Context, project, environment, key string) (FlagExperimentLayerList, error) {
	var out FlagExperimentLayerList
	err := c.do(ctx, http.MethodPost, experimentEnvPath(project, environment, "/experiment-layers"), FlagExperimentLayerRequest{Key: key}, &out)
	return out, err
}

// DeleteProjectFlagExperimentLayer deletes a layer no running experiment
// holds a slice of.
func (c *Client) DeleteProjectFlagExperimentLayer(ctx context.Context, project, environment, key string) error {
	return c.do(ctx, http.MethodDelete, experimentEnvPath(project, environment, "/experiment-layers/"+url.PathEscape(key)), nil, nil)
}

// GetProjectFlagExperimentHoldout reads the environment holdout.
func (c *Client) GetProjectFlagExperimentHoldout(ctx context.Context, project, environment string) (FlagExperimentHoldout, error) {
	var out FlagExperimentHoldout
	err := c.do(ctx, http.MethodGet, experimentEnvPath(project, environment, "/experiment-holdout"), nil, &out)
	return out, err
}

// SetProjectFlagExperimentHoldout sets the environment holdout; 0 turns it
// off.
func (c *Client) SetProjectFlagExperimentHoldout(ctx context.Context, project, environment string, basisPoints int) (FlagExperimentHoldout, error) {
	var out FlagExperimentHoldout
	err := c.do(ctx, http.MethodPut, experimentEnvPath(project, environment, "/experiment-holdout"), FlagExperimentHoldout{BasisPoints: basisPoints}, &out)
	return out, err
}

// GetProjectFlagExperimentHoldoutReport compares held-out customers with
// everyone else on event; zero from and until take the defaults.
func (c *Client) GetProjectFlagExperimentHoldoutReport(ctx context.Context, project, environment, event string, from, until time.Time) (FlagExperimentHoldoutReport, error) {
	q := url.Values{"event": {event}}
	if !from.IsZero() {
		q.Set("from", from.UTC().Format(time.RFC3339))
	}
	if !until.IsZero() {
		q.Set("until", until.UTC().Format(time.RFC3339))
	}
	var out FlagExperimentHoldoutReport
	err := c.do(ctx, http.MethodGet, experimentEnvPath(project, environment, "/experiment-holdout/report?"+q.Encode()), nil, &out)
	return out, err
}

// RuntimeFlagEvents reports conversion events for experiments
// (ADR 20261115163113). Like RuntimeFlags it requires a Client constructed
// with a workload JWT for gregale:flags.
func (c *Client) RuntimeFlagEvents(ctx context.Context, req FlagEventsRequest) (FlagEventsResponse, error) {
	var out FlagEventsResponse
	err := c.do(ctx, http.MethodPost, "/v1/runtime/flags/events", req, &out)
	return out, err
}

// ProjectFlagNotifications reads an environment's flag notification
// channels (ADR-20261125085913).
func (c *Client) ProjectFlagNotifications(ctx context.Context, project, environment string) (FlagNotificationSettings, error) {
	var out FlagNotificationSettings
	err := c.do(ctx, http.MethodGet, flagsPath(project, environment)+"/notifications", nil, &out)
	return out, err
}

// SetProjectFlagNotifications replaces an environment's flag notification
// channels; none turns them off.
func (c *Client) SetProjectFlagNotifications(ctx context.Context, project, environment string, channelIDs []string) (FlagNotificationSettings, error) {
	if channelIDs == nil {
		channelIDs = []string{}
	}
	var out FlagNotificationSettings
	err := c.do(ctx, http.MethodPut, flagsPath(project, environment)+"/notifications", FlagNotificationSettingsRequest{ChannelIDs: channelIDs}, &out)
	return out, err
}
