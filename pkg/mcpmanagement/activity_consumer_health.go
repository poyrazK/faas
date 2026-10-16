package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const activityConsumerHealthTool = "gregale_get_environment_cache_activity_consumer_health"

type activityConsumerHealthArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Owning project"`
	Environment string `json:"environment" jsonschema:"Named environment with current read consent"`
	After       string `json:"after,omitempty" jsonschema:"Opaque health continuation; restart pagination if scoped consumers change or any consumer is retired"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Consumer details per page, default 20 and maximum 50; summary covers all saved consumers in the selected scope"`
}

type activityConsumerHealthCursor struct {
	Version    int    `json:"v"`
	Binding    string `json:"b"`
	Generation string `json:"g"`
	After      string `json:"a"`
	Snapshot   string `json:"s"`
}

type activityConsumerHealthSummary struct {
	Total               int `json:"total"`
	Ready               int `json:"ready"`
	CursorExpired       int `json:"cursor_expired"`
	WithRetainedBacklog int `json:"with_retained_backlog"`
	NeedsAttention      int `json:"needs_attention"`
}

type activityConsumerHealthEntry struct {
	Consumer  string               `json:"consumer"`
	Status    string               `json:"status"`
	Revision  int64                `json:"revision"`
	UpdatedAt time.Time            `json:"updated_at"`
	Lag       *activityConsumerLag `json:"lag"`
}

type activityConsumerHealthResult struct {
	request          consumerHealthRequest
	AppID            string                        `json:"app_id"`
	ProjectID        string                        `json:"project_id"`
	EnvironmentID    string                        `json:"environment_id"`
	Scope            string                        `json:"scope"`
	Generation       string                        `json:"generation"`
	ObservedAt       time.Time                     `json:"observed_at"`
	RetentionSeconds int                           `json:"retention_seconds"`
	Summary          activityConsumerHealthSummary `json:"summary"`
	Entries          []activityConsumerHealthEntry `json:"entries"`
	NextCursor       string                        `json:"next_cursor,omitempty"`
	Basis            string                        `json:"basis"`
}

func (r activityConsumerHealthResult) checkToolRead(ctx context.Context, m *management) error {
	fresh, err := m.captureConsumerSnapshot(ctx, r.request.base, r.request.binding)
	if err != nil {
		return err
	}
	return checkConsumerSnapshot(r.request.snapshot, fresh)
}

type consumerHealthRequest struct {
	base     activityConsumerArgs
	binding  activityConsumerBinding
	snapshot consumerScopeSnapshot
	limit    int
	after    string
}

func decodeConsumerHealthCursor(raw string) (activityConsumerHealthCursor, error) {
	var token activityConsumerHealthCursor
	bad := invalid("invalid_argument", "invalid consumer health continuation; restart without after")
	if len(raw) > api.MCPActivityConsumerListCursorMaxBytes {
		return token, bad
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return token, bad
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&token) != nil || d.Decode(&struct{}{}) != io.EOF || token.Version != 1 || len(token.Binding) != 64 || len(token.Snapshot) != 64 || len(token.After) > api.MCPActivityConsumerNameMaxBytes || !activityConsumerName.MatchString(token.After) || token.Generation != "" && !consumerGeneration.MatchString(token.Generation) {
		return token, bad
	}
	return token, nil
}

func (m *management) prepareConsumerHealth(ctx context.Context, args activityConsumerHealthArgs) (consumerHealthRequest, error) {
	out := consumerHealthRequest{base: activityConsumerArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: "health"}, limit: args.Limit}
	if out.limit == 0 {
		out.limit = api.MCPActivityConsumerListDefault
	}
	if out.limit < 1 || out.limit > api.MCPActivityConsumerListMax || len(args.After) > api.MCPActivityConsumerListCursorMaxBytes {
		return out, invalid("invalid_argument", "select a consumer health page between 1 and 50 entries")
	}
	var token activityConsumerHealthCursor
	var err error
	if args.After != "" {
		token, err = decodeConsumerHealthCursor(args.After)
		if err != nil {
			return out, err
		}
	}
	out.binding, _, err = m.activityConsumerTarget(ctx, out.base)
	if err != nil {
		return out, err
	}
	if args.After != "" && token.Binding != m.consumerListBinding(out.binding) {
		return out, invalid("invalid_argument", "health continuation belongs to another connection or immutable target")
	}
	out.snapshot, err = m.captureConsumerSnapshot(ctx, out.base, out.binding)
	if err != nil {
		return out, err
	}
	if args.After != "" {
		err = checkConsumerSnapshot(consumerScopeSnapshot{generation: token.Generation, digest: token.Snapshot}, out.snapshot)
	}
	out.after = token.After
	return out, err
}

// Capture feed bounds AFTER saved records. A concurrent ACK cannot make an
// earlier checkpoint incorrectly report progress beyond the native head.
func (m *management) consumerHealthCheckpoint(ctx context.Context, req consumerHealthRequest) (api.EnvironmentCacheActivityRecovery, error) {
	b := req.binding
	point, err := m.scheduleActivityCheckpoint(ctx, appArgs{App: req.base.App, Project: req.base.Project, Environment: req.base.Environment})
	if err != nil {
		return point, scheduleWorkflowError(err)
	}
	if err = validateScheduleActivityCheckpoint(point, b.Account, b.App, b.Project, b.Environment, b.Scope); err != nil {
		return point, err
	}
	err = m.validateConsumerCursor(ctx, req.base, b, point.RestartCursor)
	var expired *activityCursorExpiredError
	if errors.As(err, &expired) {
		// The read validated fresh scoped recovery bounds when retention overtook
		// the probe. Use those bounds without returning actionable recovery data.
		point, err = expired.recovery, nil
	}
	if err != nil {
		return point, err
	}
	return point, validateScheduleActivityCheckpoint(point, b.Account, b.App, b.Project, b.Environment, b.Scope)
}

func (m *management) getActivityConsumerHealth(ctx context.Context, args activityConsumerHealthArgs) (any, error) {
	req, err := m.prepareConsumerHealth(ctx, args)
	if err != nil {
		return nil, err
	}
	point, readErr := m.consumerHealthCheckpoint(ctx, req)
	// Recheck even failed native reads. Successful output also implements a guard
	// checked after the tool wrapper's final native consent checks.
	fresh, err := m.captureConsumerSnapshot(ctx, req.base, req.binding)
	if err != nil {
		return nil, err
	}
	if err = checkConsumerSnapshot(req.snapshot, fresh); err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	return m.consumerHealthOutput(req, point, time.Now().UTC())
}

func (m *management) consumerHealthOutput(req consumerHealthRequest, point api.EnvironmentCacheActivityRecovery, at time.Time) (any, error) {
	b := req.binding
	out := activityConsumerHealthResult{request: req, AppID: b.App, ProjectID: b.Project, EnvironmentID: b.Environment, Scope: b.Scope, Generation: req.snapshot.generation, ObservedAt: at, RetentionSeconds: point.RetentionSeconds, Entries: []activityConsumerHealthEntry{}, Basis: "Saved consumers in this connection and immutable scope only. Summary covers all matching records; entries are one name-sorted page. Each call uses shared validated feed bounds and saved-record guards, not a frozen feed snapshot or retention lease. ACK age is not proof of a stalled consumer. No progress changed; watch/recover and process before explicit ACK."}
	for _, record := range req.snapshot.records {
		lag, err := consumerLag(b, record, point, at)
		if err != nil {
			return nil, err
		}
		status := "ready"
		out.Summary.Total++
		if *lag.ExpiredUnacknowledgedEvents > 0 {
			status = "cursor_expired"
			out.Summary.CursorExpired++
		} else {
			out.Summary.Ready++
		}
		if *lag.RetainedPendingEvents > 0 {
			out.Summary.WithRetainedBacklog++
		}
		if *lag.RetainedPendingEvents > 0 || *lag.ExpiredUnacknowledgedEvents > 0 {
			out.Summary.NeedsAttention++
		}
		if record.Binding.Consumer > req.after {
			out.Entries = append(out.Entries, activityConsumerHealthEntry{Consumer: record.Binding.Consumer, Status: status, Revision: record.Revision, UpdatedAt: record.UpdatedAt, Lag: lag})
		}
	}
	if len(out.Entries) > req.limit {
		out.Entries = out.Entries[:req.limit]
		data, _ := json.Marshal(activityConsumerHealthCursor{Version: 1, Binding: m.consumerListBinding(b), Generation: req.snapshot.generation, After: out.Entries[req.limit-1].Consumer, Snapshot: req.snapshot.digest})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(data)
		if len(out.NextCursor) > api.MCPActivityConsumerListCursorMaxBytes {
			return nil, invalid("response_too_large", "health continuation exceeds its bound")
		}
	}
	return out, nil
}
