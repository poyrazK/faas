package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsIntentStore = (*PgStore)(nil)

type gitOpsIntentApp struct {
	ID            string                        `json:"id"`
	Slug          string                        `json:"slug"`
	Variables     map[string]string             `json:"variables"`
	Routes        *api.EnvironmentRouteContract `json:"routes"`
	Policies      *[]ProjectEnvironmentEdgeRule `json:"policies"`
	VariableCount int                           `json:"variable_count"`
}

type gitOpsIntentResource struct {
	Resource string `json:"resource"`
	AppID    string `json:"app_id"`
}

type gitOpsIntentSnapshot struct {
	Plan          api.Plan                    `json:"plan"`
	SourceID      string                      `json:"source_id"`
	Prune         bool                        `json:"prune"`
	Version       int64                       `json:"version"`
	Project       string                      `json:"project"`
	Environment   string                      `json:"environment"`
	Configuration map[string]json.RawMessage  `json:"configuration"`
	Resources     []gitOpsIntentResource      `json:"resources"`
	Apps          []gitOpsIntentApp           `json:"apps"`
	Owners        []environmentsync.Ownership `json:"owners"`
	Overrides     []environmentsync.Override  `json:"overrides"`
}

func canonicalGitOpsValue(value json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

// All scoped intent and ownership are read at one statement snapshot.
func readEnvironmentGitOpsIntent(ctx context.Context, db sqlc.DBTX, source EnvironmentGitSource, desired environmentsync.DesiredState) (EnvironmentGitOpsObservation, gitOpsIntentSnapshot, error) {
	raw, err := sqlc.New().ObserveEnvironmentGitOpsIntent(ctx, db, sqlc.ObserveEnvironmentGitOpsIntentParams{
		SourceID: mustPgUUID(source.ID), AccountID: mustPgUUID(source.AccountID),
	})
	if err != nil {
		return EnvironmentGitOpsObservation{}, gitOpsIntentSnapshot{}, mapErr(err)
	}
	var snapshot gitOpsIntentSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return EnvironmentGitOpsObservation{}, snapshot, err
	}
	observation, err := compileGitOpsObservation(snapshot, desired)
	return observation, snapshot, err
}

func compileGitOpsObservation(snapshot gitOpsIntentSnapshot, desired environmentsync.DesiredState) (EnvironmentGitOpsObservation, error) {
	out := EnvironmentGitOpsObservation{
		State:  environmentsync.ObservedState{Version: snapshot.Version, ResourceIDs: map[string]string{}},
		Owners: snapshot.Owners, Overrides: snapshot.Overrides,
	}
	if desired.Definition.Project != snapshot.Project || desired.Definition.Environment != snapshot.Environment {
		return out, ErrInvalidArgument
	}
	add := func(resource, path string, value any) {
		raw, _ := json.Marshal(value)
		out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: path, Value: raw})
	}
	for key, value := range snapshot.Configuration {
		canonical, err := canonicalGitOpsValue(value)
		if err != nil {
			return out, err
		}
		out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: "environment", Path: "configuration/" + key, Value: canonical})
	}
	byID, bySlug := map[string]gitOpsIntentApp{}, map[string]gitOpsIntentApp{}
	for _, app := range snapshot.Apps {
		byID[app.ID], bySlug[app.Slug] = app, app
	}
	mapped := map[string]bool{}
	for _, resource := range snapshot.Resources {
		mapped[resource.Resource] = true
		out.State.ResourceIDs[resource.Resource] = resource.AppID
	}
	for name, workload := range desired.Definition.Workloads {
		resource := "workload/" + name
		if !mapped[resource] && workload.App != "" {
			out.State.ResourceIDs[resource] = bySlug[workload.App].ID
		}
		if workload.App != "" && out.State.ResourceIDs[resource] != "" && byID[out.State.ResourceIDs[resource]].Slug != workload.App {
			out.State.Unsupported = append(out.State.Unsupported, resource+": mapped app identity differs from the definition")
		}
		if out.State.ResourceIDs[resource] == "" {
			out.State.Unsupported = append(out.State.Unsupported, resource+": workload creation adapter is not available")
		} else if _, exists := byID[out.State.ResourceIDs[resource]]; !exists {
			out.State.Unsupported = append(out.State.Unsupported, resource+": mapped workload is absent; restoration adapter is not available")
		}
		limits, ok := api.LimitsFor(snapshot.Plan)
		if !ok {
			return out, ErrInvalidArgument
		}
		app := byID[out.State.ResourceIDs[resource]]
		count := app.VariableCount
		for key, value := range workload.Variables {
			if _, present := app.Variables[key]; !present {
				count++
			}
			if (api.PutAppEnvRequest{Value: value}).Validate(limits.EnvValueMaxBytes) != nil {
				out.State.Unsupported = append(out.State.Unsupported, resource+": variable exceeds plan value limit")
			}
		}
		if snapshot.Prune {
			for _, owner := range snapshot.Owners {
				if owner.Manager != snapshot.SourceID || owner.Resource != resource || !strings.HasPrefix(owner.Path, "variables/") {
					continue
				}
				key := strings.TrimPrefix(owner.Path, "variables/")
				_, wanted := workload.Variables[key]
				_, present := app.Variables[key]
				activeOverride := false
				for _, override := range snapshot.Overrides {
					if override.Resource == resource && override.Path == owner.Path && override.ExpiresAt.After(time.Now()) {
						activeOverride = true
						break
					}
				}
				if !wanted && present && !activeOverride {
					count--
				}
			}
		}
		if count > limits.EnvVarsMax {
			out.State.Unsupported = append(out.State.Unsupported, resource+": variable count exceeds plan quota across environments")
		}
		if workload.Policies != nil && len(*workload.Policies) > min(api.EnvironmentGitOpsMaxPolicies, limits.EdgeRulesPerApp) {
			out.State.Unsupported = append(out.State.Unsupported, resource+": policy count exceeds plan quota")
		}
	}
	for resource, id := range out.State.ResourceIDs {
		app, exists := byID[id]
		if !exists {
			continue
		}
		add(resource, "presence", true)
		for key, value := range app.Variables {
			add(resource, "variables/"+key, value)
		}
		workload := api.EnvironmentWorkload{Routes: app.Routes}
		if app.Policies != nil {
			policies := make([]api.EnvironmentPolicy, 0, len(*app.Policies))
			for i, rule := range *app.Policies {
				name := rule.Name
				if name == "" {
					name = fmt.Sprintf("existing-rule-%d", i+1)
				}
				var action any
				if rule.Kind == EdgeRuleKindHeaders {
					action = rule.Action.Headers
				} else {
					action = rule.Action.CORS
				}
				raw, _ := json.Marshal(action)
				enabled := rule.Enabled
				policies = append(policies, api.EnvironmentPolicy{Name: name, Kind: string(rule.Kind), MatchPath: rule.MatchPath,
					MatchMethods: rule.MatchMethods, MatchHeaders: rule.MatchHeaders, Priority: rule.Priority, Enabled: &enabled, Action: raw})
			}
			workload.Policies = &policies
		}
		if workload.Routes != nil || workload.Policies != nil {
			compiled, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
				Project: snapshot.Project, Environment: snapshot.Environment, Workloads: map[string]api.EnvironmentWorkload{"observed": workload}})
			if err != nil {
				return out, fmt.Errorf("invalid observed route or policy intent: %w", ErrInvalidArgument)
			}
			for _, field := range compiled.Fields {
				if field.Path != "presence" {
					field.Resource = resource
					out.State.Fields = append(out.State.Fields, field)
				}
			}
		}
	}
	for _, field := range desired.Fields {
		if !gitOpsScopedFieldSupported(field.Path) {
			out.State.Unsupported = append(out.State.Unsupported, field.Key()+": scoped adapter is not available")
		}
	}
	for _, owner := range snapshot.Owners {
		if !gitOpsScopedFieldSupported(owner.Path) {
			out.State.Unsupported = append(out.State.Unsupported, owner.Key()+": scoped pruning adapter is not available")
		}
		if snapshot.Prune && owner.Manager == snapshot.SourceID && owner.Path == "presence" && strings.HasPrefix(owner.Resource, "workload/") {
			if _, wanted := desired.Definition.Workloads[strings.TrimPrefix(owner.Resource, "workload/")]; !wanted {
				out.State.Unsupported = append(out.State.Unsupported, owner.Key()+": workload pruning adapter is not available")
			}
		}
	}
	return out, nil
}

func gitOpsScopedFieldSupported(path string) bool {
	return path == "presence" || path == "routes" || path == "policies" || strings.HasPrefix(path, "variables/") || strings.HasPrefix(path, "configuration/")
}

func desiredEnvironmentRevision(revision EnvironmentDesiredRevision) (environmentsync.DesiredState, error) {
	var definition api.EnvironmentDefinition
	if json.Unmarshal(revision.Definition, &definition) != nil {
		return environmentsync.DesiredState{}, ErrInvalidArgument
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil || desired.Digest != revision.Digest {
		return environmentsync.DesiredState{}, ErrInvalidArgument
	}
	return desired, nil
}

func environmentGitOpsPlan(source EnvironmentGitSource, revision EnvironmentDesiredRevision, desired environmentsync.DesiredState, observed EnvironmentGitOpsObservation, adopt bool) (environmentsync.Plan, error) {
	return environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{
		Manager: source.ID, Revision: revision.ID, CommitSHA: revision.CommitSHA, Generation: source.Generation,
		Adopt: adopt, Prune: source.Spec.Prune, Now: time.Now().UTC(), Overrides: observed.Overrides,
	})
}

func (s *PgStore) ObserveEnvironmentGitOps(ctx context.Context, lease EnvironmentGitOpsLease, desired environmentsync.DesiredState) (EnvironmentGitOpsObservation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EnvironmentGitOpsObservation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockEnvironmentGitOps(ctx, tx, lease, time.Now().UTC()); err != nil {
		return EnvironmentGitOpsObservation{}, err
	}
	observation, _, err := readEnvironmentGitOpsIntent(ctx, tx, lease.Source, desired)
	return observation, err
}

func (s *PgStore) PreviewEnvironmentGitOpsAdoption(ctx context.Context, accountID, sourceID string) (environmentsync.Plan, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return environmentsync.Plan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	source, revision, desired, err := lockApprovedEnvironmentGitOps(ctx, tx, accountID, sourceID)
	if err != nil {
		return environmentsync.Plan{}, err
	}
	observed, _, err := readEnvironmentGitOpsIntent(ctx, tx, source, desired)
	if err != nil {
		return environmentsync.Plan{}, err
	}
	return environmentGitOpsPlan(source, revision, desired, observed, true)
}

func lockApprovedEnvironmentGitOps(ctx context.Context, tx pgx.Tx, accountID, sourceID string) (EnvironmentGitSource, EnvironmentDesiredRevision, environmentsync.DesiredState, error) {
	q := sqlc.New()
	rawSource, err := q.LockEnvironmentGitSource(ctx, tx, sqlc.LockEnvironmentGitSourceParams{AccountID: mustPgUUID(accountID), SourceID: mustPgUUID(sourceID)})
	if err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, environmentsync.DesiredState{}, mapErr(err)
	}
	if !rawSource.ApprovedRevisionID.Valid || rawSource.Suspended {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, environmentsync.DesiredState{}, ErrConflict
	}
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, rawSource.ID)
	if err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, environmentsync.DesiredState{}, mapErr(err)
	}
	rawRevision, err := q.GetEnvironmentDesiredRevision(ctx, tx, sqlc.GetEnvironmentDesiredRevisionParams{SourceID: rawSource.ID, RevisionID: rawSource.ApprovedRevisionID})
	if err != nil {
		return EnvironmentGitSource{}, EnvironmentDesiredRevision{}, environmentsync.DesiredState{}, mapErr(err)
	}
	source, revision := environmentGitSourceFromSQL(rawSource, scope.EnvironmentSlug), environmentRevisionFromSQL(rawRevision)
	desired, err := desiredEnvironmentRevision(revision)
	return source, revision, desired, err
}

func (s *PgStore) AdoptEnvironmentGitOps(ctx context.Context, accountID, sourceID, reviewedHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	source, revision, desired, err := lockApprovedEnvironmentGitOps(ctx, tx, accountID, sourceID)
	if err != nil {
		return err
	}
	observed, _, err := readEnvironmentGitOpsIntent(ctx, tx, source, desired)
	if err != nil {
		return err
	}
	plan, err := environmentGitOpsPlan(source, revision, desired, observed, true)
	if err != nil {
		return err
	}
	if !plan.CanApply() || plan.Hash != reviewedHash {
		return ErrConflict
	}
	q := sqlc.New()
	for _, change := range plan.Changes {
		if change.Action != "adopt" {
			continue
		}
		if change.Path == "presence" {
			count, err := q.BindEnvironmentGitOpsResource(ctx, tx, sqlc.BindEnvironmentGitOpsResourceParams{SourceID: mustPgUUID(source.ID), Resource: change.Resource, AppID: mustPgUUID(observed.State.ResourceIDs[change.Resource])})
			if err != nil {
				return mapErr(err)
			}
			if count != 1 {
				return ErrConflict
			}
		}
		count, err := q.OwnEnvironmentGitOpsField(ctx, tx, sqlc.OwnEnvironmentGitOpsFieldParams{SourceID: mustPgUUID(source.ID), Resource: change.Resource, FieldPath: change.Path, Value: change.After})
		if err != nil {
			return mapErr(err)
		}
		if count != 1 {
			return ErrConflict
		}
	}
	if err := q.TouchEnvironmentGitOpsIntent(ctx, tx, mustPgUUID(source.ID)); err != nil {
		return err
	}
	if err := recordGitOpsEvent(ctx, tx, source.ID, accountID, "adopt", plan); err != nil {
		return err
	}
	if err := q.EnqueueEnvironmentGitOps(ctx, tx, sqlc.EnqueueEnvironmentGitOpsParams{SourceID: mustPgUUID(source.ID), Generation: source.Generation, NextAttemptAt: gitOpsTime(time.Now())}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ApplyEnvironmentGitOps(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentGitOpsStep, error) {
	return s.applyEnvironmentGitOps(ctx, lease, reviewed, nil, false)
}

func (s *PgStore) ApplyEnvironmentGitOpsWithEffects(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan, effects []EnvironmentGitOpsEffectSpec) ([]EnvironmentGitOpsStep, error) {
	return s.applyEnvironmentGitOps(ctx, lease, reviewed, effects, true)
}

func (s *PgStore) applyEnvironmentGitOps(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan, effects []EnvironmentGitOpsEffectSpec, requireEffects bool) ([]EnvironmentGitOpsStep, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockEnvironmentGitOps(ctx, tx, lease, time.Now()); err != nil {
		return nil, err
	}
	if lease.Source.Spec.Mode != "enforce" {
		return nil, ErrConflict
	}
	desired, err := desiredEnvironmentRevision(lease.Revision)
	if err != nil {
		return nil, err
	}
	observed, snapshot, err := readEnvironmentGitOpsIntent(ctx, tx, lease.Source, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(lease.Source, lease.Revision, desired, observed, false)
	if err != nil {
		return nil, err
	}
	if !plan.CanApply() || plan.Hash != reviewed.Hash {
		return nil, ErrConflict
	}
	if requireEffects {
		effects, err = validateGitOpsEffects(plan, observed, effects)
		if err != nil {
			return nil, err
		}
	}
	q := sqlc.New()
	if _, err := q.SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken); err != nil {
		return nil, err
	}
	steps := []EnvironmentGitOpsStep{}
	configChanged := false
	for _, change := range plan.Changes {
		if change.Action == "keep" || change.Action == "retain_unmanaged" || change.Action == "overridden" {
			continue
		}
		if change.Path == "presence" {
			return nil, ErrConflict
		} // creation/pruning belongs to the staged workload graph
		if strings.HasPrefix(change.Path, "configuration/") {
			key := strings.TrimPrefix(change.Path, "configuration/")
			if change.Action == "remove" {
				delete(snapshot.Configuration, key)
			} else {
				snapshot.Configuration[key] = change.After
			}
			configChanged = true
		} else if err := applyEnvironmentGitOpsScopedField(ctx, tx, lease.Source, observed.State.ResourceIDs[change.Resource], change); err != nil {
			return nil, mapErr(err)
		}
		if change.Action == "remove" {
			err = q.ReleaseEnvironmentGitOpsField(ctx, tx, sqlc.ReleaseEnvironmentGitOpsFieldParams{SourceID: mustPgUUID(lease.Source.ID), Resource: change.Resource, FieldPath: change.Path})
		} else {
			var count int64
			count, err = q.OwnEnvironmentGitOpsField(ctx, tx, sqlc.OwnEnvironmentGitOpsFieldParams{SourceID: mustPgUUID(lease.Source.ID), Resource: change.Resource, FieldPath: change.Path, Value: change.After})
			if err == nil && count != 1 {
				err = ErrConflict
			}
		}
		if err != nil {
			return nil, mapErr(err)
		}
		steps = append(steps, EnvironmentGitOpsStep{Resource: change.Resource, Path: change.Path, Action: change.Action, Status: "applied"})
	}
	if configChanged {
		raw, _ := json.Marshal(snapshot.Configuration)
		values, hash, err := api.NormalizeProjectEnvironmentConfig(raw)
		if err != nil {
			return nil, ErrInvalidArgument
		}
		if err := q.InsertEnvironmentGitOpsConfig(ctx, tx, sqlc.InsertEnvironmentGitOpsConfigParams{AccountID: mustPgUUID(lease.Source.AccountID), ProjectID: mustPgUUID(lease.Source.ProjectID), Environment: lease.Source.EnvironmentSlug, Hash: hash, Values: values}); err != nil {
			return nil, mapErr(err)
		}
	}
	rawPlan, _ := json.Marshal(plan)
	rawSteps, _ := json.Marshal(steps)
	if len(steps) > 0 {
		if err := q.TouchEnvironmentGitOpsIntent(ctx, tx, mustPgUUID(lease.Source.ID)); err != nil {
			return nil, err
		}
	}
	for _, effect := range effects {
		if err := q.InsertEnvironmentGitOpsEffect(ctx, tx, sqlc.InsertEnvironmentGitOpsEffectParams{
			SourceID: mustPgUUID(lease.Source.ID), RevisionID: mustPgUUID(lease.Revision.ID), PlanHash: plan.Hash,
			AppID: mustPgUUID(effect.AppID), Kind: effect.Kind, GatewayGeneration: effect.GatewayGeneration,
			MatchHosts: effect.MatchHosts, ExpectedNodes: effect.ExpectedNodes,
		}); err != nil {
			return nil, mapErr(err)
		}
	}
	if requireEffects {
		for _, appID := range gitOpsChangedVariableApps(plan, observed.State.ResourceIDs) {
			if err := insertGitOpsRuntimeEffect(ctx, tx, lease, plan, appID, time.Unix(0, 0).UTC()); err != nil {
				return nil, err
			}
		}
	}
	count, err := q.SaveEnvironmentGitOpsProgress(ctx, tx, sqlc.SaveEnvironmentGitOpsProgressParams{RunID: mustPgUUID(lease.RunID), SourceID: mustPgUUID(lease.Source.ID), LeaseToken: lease.LeaseToken, Plan: rawPlan, Steps: rawSteps})
	if err != nil {
		return nil, mapErr(err)
	}
	if count != 1 {
		return nil, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return steps, nil
}

func applyEnvironmentGitOpsScopedField(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource, appID string, change environmentsync.Change) error {
	if appID == "" {
		return ErrConflict
	}
	q := sqlc.New()
	account, project, app := mustPgUUID(source.AccountID), mustPgUUID(source.ProjectID), mustPgUUID(appID)
	switch {
	case strings.HasPrefix(change.Path, "variables/"):
		key := strings.TrimPrefix(change.Path, "variables/")
		// The freshness stamp and cache invalidation commit with the env value,
		// so a crashed worker cannot leave a restorable cache with old variables.
		if err := q.InvalidateEnvironmentGitOpsRuntimeConfig(ctx, tx, app); err != nil {
			return err
		}
		if change.Action == "remove" {
			return q.DeleteEnvironmentGitOpsVariable(ctx, tx, sqlc.DeleteEnvironmentGitOpsVariableParams{AccountID: account, AppID: app, Scope: source.EnvironmentSlug, Key: key})
		}
		var value string
		if json.Unmarshal(change.After, &value) != nil {
			return ErrInvalidArgument
		}
		return q.PutEnvironmentGitOpsVariable(ctx, tx, sqlc.PutEnvironmentGitOpsVariableParams{AccountID: account, AppID: app, Scope: source.EnvironmentSlug, Key: key, Value: value})
	case change.Path == "routes":
		if change.Action == "remove" {
			return q.DeleteEnvironmentGitOpsRoutes(ctx, tx, sqlc.DeleteEnvironmentGitOpsRoutesParams{AccountID: account, AppID: app, Environment: source.EnvironmentSlug})
		}
		var routes api.EnvironmentRouteContract
		if json.Unmarshal(change.After, &routes) != nil {
			return ErrInvalidArgument
		}
		raw, _ := json.Marshal(routes.DeclaredRoutes)
		return q.PutEnvironmentGitOpsRoutes(ctx, tx, sqlc.PutEnvironmentGitOpsRoutesParams{AccountID: account, ProjectID: project, AppID: app, Environment: source.EnvironmentSlug, Enforced: routes.OnlyAllowDeclaredRoutes, Routes: raw})
	case change.Path == "policies":
		if change.Action == "remove" {
			return q.DeleteEnvironmentGitOpsPolicies(ctx, tx, sqlc.DeleteEnvironmentGitOpsPoliciesParams{AccountID: account, AppID: app, Environment: source.EnvironmentSlug})
		}
		rules, err := decodeEnvironmentGitOpsPolicies(change.After)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(rules)
		return q.PutEnvironmentGitOpsPolicies(ctx, tx, sqlc.PutEnvironmentGitOpsPoliciesParams{AccountID: account, ProjectID: project, AppID: app, Environment: source.EnvironmentSlug, Rules: raw})
	default:
		return ErrInvalidArgument
	}
}

func decodeEnvironmentGitOpsPolicies(raw json.RawMessage) ([]ProjectEnvironmentEdgeRule, error) {
	var policies []api.EnvironmentPolicy
	if json.Unmarshal(raw, &policies) != nil {
		return nil, ErrInvalidArgument
	}
	rules := make([]ProjectEnvironmentEdgeRule, 0, len(policies))
	for _, policy := range policies {
		action := EdgeRuleAction{Kind: EdgeRuleKind(policy.Kind)}
		if policy.Kind == "headers" {
			action.Headers = &EdgeRuleHeadersAction{}
			if json.Unmarshal(policy.Action, action.Headers) != nil {
				return nil, ErrInvalidArgument
			}
		} else if policy.Kind == "cors" {
			action.CORS = &EdgeRuleCORSAction{}
			if json.Unmarshal(policy.Action, action.CORS) != nil {
				return nil, ErrInvalidArgument
			}
		} else {
			return nil, ErrInvalidArgument
		}
		rules = append(rules, ProjectEnvironmentEdgeRule{Name: policy.Name, Kind: action.Kind, MatchPath: policy.MatchPath,
			MatchMethods: policy.MatchMethods, MatchHeaders: policy.MatchHeaders, Priority: policy.Priority,
			Enabled: policy.Enabled != nil && *policy.Enabled, Action: action})
	}
	if !validProjectEnvironmentEdgeRules(rules) {
		return nil, ErrInvalidArgument
	}
	return rules, nil
}
