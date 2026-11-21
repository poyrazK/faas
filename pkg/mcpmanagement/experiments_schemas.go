package mcpmanagement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Flag experiment changes and event schema registration
// (ADR-20261119190000). Neither belongs to one app, so their reviewed plans
// live in this connection's memory, like flag plans: a plan pins what it
// reviewed and an apply sends it once, under an idempotency key derived from
// the plan. apid asks a person before production experiment starts and
// ships, and before every schema registration.

const (
	maxReviewedPlans = 20
	reviewedPlanTTL  = 15 * time.Minute
)

// reviewedPlan is a reviewed change that is not an app operation.
type reviewedPlan struct {
	ID            string          `json:"plan_id"`
	Kind          string          `json:"kind"`
	AccountID     string          `json:"account_id"`
	Project       string          `json:"project,omitempty"`
	ProjectID     string          `json:"project_id,omitempty"`
	Environment   string          `json:"environment,omitempty"`
	EnvironmentID string          `json:"environment_id,omitempty"`
	Request       json.RawMessage `json:"request"`
	Review        any             `json:"review,omitempty"`
	Effect        string          `json:"effect"`
	Reason        string          `json:"reason"`
	ExpiresAt     time.Time       `json:"expires_at"`
	receipt       any
}

type planExperimentArgs struct {
	Project     string         `json:"project" jsonschema:"Project slug"`
	Environment string         `json:"environment" jsonschema:"Named project environment, e.g. production"`
	Op          string         `json:"op" jsonschema:"create, start, stop or ship"`
	Key         string         `json:"key,omitempty" jsonschema:"start, stop, ship: the experiment"`
	Create      map[string]any `json:"create,omitempty" jsonschema:"create: the experiment as POST .../experiments takes it: key, flag, rule_id, control, metric_event, metric_kind, guardrails, ... (start it with op start)"`
	Variant     string         `json:"variant,omitempty" jsonschema:"ship: the variant every customer of the rule gets"`
	Reason      string         `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type experimentChange struct {
	Op      string                           `json:"op"`
	Key     string                           `json:"key,omitempty"`
	Create  *api.FlagExperimentCreateRequest `json:"create,omitempty"`
	Variant string                           `json:"variant,omitempty"`
}

type schemaListArgs struct {
	Source string `json:"source,omitempty" jsonschema:"Event source such as app:orders"`
	Type   string `json:"type,omitempty" jsonschema:"Event type such as order.created"`
}

type planSchemaArgs struct {
	Source  string           `json:"source" jsonschema:"Event source"`
	Type    string           `json:"type" jsonschema:"Event type"`
	Version string           `json:"version" jsonschema:"New schema version, such as 2"`
	Schema  map[string]any   `json:"schema" jsonschema:"JSON Schema for the event data"`
	Samples []map[string]any `json:"samples,omitempty" jsonschema:"Example event data to validate against the schema; up to 20"`
	Reason  string           `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

func registerExperimentsSchemas(s *mcp.Server, m *management) {
	register(s, m, "gregale_plan_experiment_change", "Review creating, starting, stopping or shipping a flag experiment. Ship gives every customer of the rule one variant. Changes nothing; apply with gregale_apply_reviewed_change. In production, start and ship ask a person first.", m.planExperimentChange)
	register(s, m, "gregale_list_event_schemas", "List registered event schemas and their versions, optionally for one source and type. Read-only.", m.listEventSchemas)
	register(s, m, "gregale_plan_event_schema_registration", "Review registering a new event schema version: shows which subscriptions accept or exclude it and validates samples and retained events against it. Changes nothing; apply with gregale_apply_reviewed_change, which asks a person first.", m.planEventSchemaRegistration)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_apply_reviewed_change", "Apply a reviewed experiment change or event schema registration plan. Retry the same plan_id after an uncertain response.", false, m.applyReviewedChange)
	}
}

func (m *management) saveReviewedPlan(p *reviewedPlan) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, old := range m.reviewedPlans {
		if now.After(old.ExpiresAt) {
			delete(m.reviewedPlans, id)
		}
	}
	if len(m.reviewedPlans) >= maxReviewedPlans {
		return nil, invalid("plan_capacity", "connection has too many active plans; wait for expiration")
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	p.ID = hex.EncodeToString(id)
	p.ExpiresAt = now.Add(reviewedPlanTTL)
	m.reviewedPlans[p.ID] = p
	if err := m.persistMemoryPlansLocked(); err != nil {
		delete(m.reviewedPlans, p.ID)
		return nil, err
	}
	return *p, nil
}

func (m *management) planExperimentChange(ctx context.Context, a planExperimentArgs) (any, error) {
	if err := validateReason("experiment", a.Reason); err != nil {
		return nil, err
	}
	if err := validateFlagExperimentScope(a.Project, a.Environment); err != nil {
		return nil, err
	}
	project, env, err := m.environmentAccess(ctx, a.Project, a.Environment, false, false)
	if err != nil {
		return nil, err
	}
	req := experimentChange{Op: a.Op, Key: a.Key}
	var review any
	effect := ""
	switch a.Op {
	case "create":
		if a.Key != "" || a.Variant != "" {
			return nil, invalid("invalid_argument", "create takes only create")
		}
		var create api.FlagExperimentCreateRequest
		if err := strictDecode(a.Create, &create, "create"); err != nil {
			return nil, err
		}
		if err := validateSegment(create.Key, "create.key"); err != nil {
			return nil, err
		}
		// Starting is its own reviewed step, so production starts ask a person.
		create.Start = false
		req.Create, req.Key = &create, create.Key
		review = create
		effect = "Records the experiment on the flag rule without starting it; no customer sees a change."
	case "start", "stop", "ship":
		if err := validateSegment(a.Key, "key"); err != nil {
			return nil, err
		}
		exp, err := m.client.GetProjectFlagExperiment(ctx, a.Project, a.Environment, a.Key)
		if err != nil {
			return nil, err
		}
		view := map[string]any{"experiment": exp}
		if a.Op == "ship" {
			if err := validateSegment(a.Variant, "variant"); err != nil {
				return nil, err
			}
			req.Variant = a.Variant
			if res, err := m.client.ProjectFlagExperimentResults(ctx, a.Project, a.Environment, a.Key); err == nil {
				view["results"] = res
			}
		} else if a.Variant != "" {
			return nil, invalid("invalid_argument", "variant is for ship")
		}
		review = view
		effect = map[string]string{
			"start": "Starts assigning the rule's customers to the experiment's variants and measuring them.",
			"stop":  "Stops the experiment; the rule keeps serving its current split.",
			"ship":  "Gives every customer of the rule the shipped variant and ends the experiment.",
		}[a.Op]
	default:
		return nil, invalid("invalid_argument", "op must be create, start, stop or ship")
	}
	if a.Create != nil && a.Op != "create" {
		return nil, invalid("invalid_argument", "create is for op create")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return m.saveReviewedPlan(&reviewedPlan{Kind: "experiment_change", AccountID: account.ID, Project: a.Project, ProjectID: project.ID, Environment: a.Environment, EnvironmentID: env.ID, Request: raw, Review: review, Effect: effect, Reason: a.Reason})
}

func (m *management) listEventSchemas(ctx context.Context, a schemaListArgs) (any, error) {
	for field, v := range map[string]string{"source": a.Source, "type": a.Type} {
		if v != "" {
			if err := validEventName(v, field); err != nil {
				return nil, err
			}
		}
	}
	schemas, err := m.client.ListEventSchemas(ctx, a.Source, a.Type)
	if err != nil {
		return nil, err
	}
	return map[string]any{"schemas": schemas}, nil
}

// validEventName bounds an event source, type or version: printable, no
// spaces, at most 128 bytes.
func validEventName(v, field string) error {
	if v == "" || len(v) > 128 {
		return invalid("invalid_argument", field+" must be 1 to 128 characters")
	}
	for _, r := range v {
		if r <= ' ' || r == 0x7f {
			return invalid("invalid_argument", field+" holds no spaces or control characters")
		}
	}
	return nil
}

func (m *management) planEventSchemaRegistration(ctx context.Context, a planSchemaArgs) (any, error) {
	if err := validateReason("schema", a.Reason); err != nil {
		return nil, err
	}
	if m.opts.AllowedApps != nil {
		return nil, invalid("app_access_denied", "event schemas are shared by every app; registering one needs a connection to every app")
	}
	for field, v := range map[string]string{"source": a.Source, "type": a.Type, "version": a.Version} {
		if err := validEventName(v, field); err != nil {
			return nil, err
		}
	}
	if len(a.Schema) == 0 || len(a.Samples) > 20 {
		return nil, invalid("invalid_argument", "schema is required and samples hold at most 20 events")
	}
	schema, err := json.Marshal(a.Schema)
	if err != nil {
		return nil, invalid("invalid_argument", "schema is not JSON")
	}
	preview := api.EventSchemaRolloutRequest{Source: a.Source, Type: a.Type, Version: a.Version, Schema: schema}
	for _, s := range a.Samples {
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, invalid("invalid_argument", "a sample is not JSON")
		}
		preview.Samples = append(preview.Samples, raw)
	}
	rollout, err := m.client.PreviewEventSchemaRollout(ctx, preview)
	if err != nil {
		return nil, err
	}
	if rollout.SampleInvalidCount > 0 {
		return nil, invalid("samples_invalid", "some samples do not match the schema; fix the schema or the samples and plan again")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(api.RegisterEventSchemaRequest{Source: a.Source, Type: a.Type, Version: a.Version, Schema: schema})
	if err != nil {
		return nil, err
	}
	effect := "Registers the schema version. Subscriptions pinned to other versions keep excluding it; publishers' events are validated against the version they name."
	return m.saveReviewedPlan(&reviewedPlan{Kind: "schema_registration", AccountID: account.ID, Request: raw, Review: rollout, Effect: effect, Reason: a.Reason})
}

func (m *management) applyReviewedChange(ctx context.Context, a applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.reviewedPlans[a.PlanID]
	if p == nil {
		return nil, invalid("invalid_plan", "unknown or expired plan; review a new one")
	}
	if p.receipt != nil {
		return p.receipt, nil
	}
	if time.Now().After(p.ExpiresAt) {
		delete(m.reviewedPlans, a.PlanID)
		return nil, invalid("plan_expired", "plan expired; review a new one")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID {
		return nil, invalid("account_changed", "account identity changed; create a new connection and plan")
	}
	ctx = api.ContextWithIdempotencyKey(ctx, "mcp-reviewed-"+p.ID)
	var out any
	switch p.Kind {
	case "experiment_change":
		out, err = m.runExperimentChange(ctx, p)
	case "schema_registration":
		var req api.RegisterEventSchemaRequest
		if err = json.Unmarshal(p.Request, &req); err == nil {
			out, err = m.client.RegisterEventSchema(ctx, req)
		}
	default:
		err = invalid("invalid_plan", "unsupported plan kind")
	}
	if err != nil {
		return nil, err
	}
	p.receipt = map[string]any{"plan_id": p.ID, "kind": p.Kind, "result": out}
	// The write succeeded under its idempotency key; a failed journal only
	// costs the replayed receipt.
	_ = m.persistMemoryPlansLocked()
	return p.receipt, nil
}

func (m *management) runExperimentChange(ctx context.Context, p *reviewedPlan) (any, error) {
	project, env, err := m.environmentAccess(ctx, p.Project, p.Environment, true, false)
	if err != nil {
		return nil, err
	}
	if project.ID != p.ProjectID || env.ID != p.EnvironmentID {
		return nil, invalid("plan_stale", "project or environment identity changed; review a new plan")
	}
	var req experimentChange
	if err := json.Unmarshal(p.Request, &req); err != nil {
		return nil, err
	}
	switch req.Op {
	case "create":
		return m.client.CreateProjectFlagExperiment(ctx, p.Project, p.Environment, *req.Create)
	case "start":
		return m.client.StartProjectFlagExperiment(ctx, p.Project, p.Environment, req.Key)
	case "stop":
		return m.client.StopProjectFlagExperiment(ctx, p.Project, p.Environment, req.Key)
	case "ship":
		return m.client.ShipProjectFlagExperiment(ctx, p.Project, p.Environment, req.Key, req.Variant)
	}
	return nil, invalid("invalid_plan", "unsupported experiment change")
}
