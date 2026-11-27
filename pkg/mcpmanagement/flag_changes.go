package mcpmanagement

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
)

// Feature flag changes (ADR-20261117215858 §3). A plan applies an ordered
// list of edits, the CLI's rules from pkg/flags, to the configuration version
// it read; apply publishes exactly that configuration with the version as
// its guard, so a configuration that changed since the plan is refused
// rather than overwritten. Plans live in this connection's memory: after a
// bridge restart the agent plans again, and the version guard keeps a
// repeated publish from applying twice. The kill switch needs no plan.

const (
	maxFlagPlans      = 16
	maxFlagChanges    = 20
	flagPlanTTL       = 15 * time.Minute
	flagsConflictCode = "flags_version_conflict"
)

type flagScopeArgs struct {
	Project     string `json:"project" jsonschema:"Project slug"`
	Environment string `json:"environment" jsonschema:"Named project environment, e.g. production"`
}

type flagChange struct {
	Op          string            `json:"op" jsonschema:"One of create, set, enable, disable, kill, revive, set_percent, add_rule, remove_rule, delete"`
	Key         string            `json:"key" jsonschema:"Flag key: lowercase letter, then up to 63 of a-z 0-9 _ -"`
	Variants    []string          `json:"variants,omitempty" jsonschema:"create: at least two variant keys for a variant flag; omit for a boolean flag"`
	Default     *string           `json:"default,omitempty" jsonschema:"create or set: true/false, or a variant key"`
	Description *string           `json:"description,omitempty" jsonschema:"create or set: description"`
	Owner       *string           `json:"owner,omitempty" jsonschema:"create or set: team or email that decides about the flag"`
	Expires     *string           `json:"expires,omitempty" jsonschema:"create or set: expiry date YYYY-MM-DD, or never to clear"`
	Percent     string            `json:"percent,omitempty" jsonschema:"set_percent: 0 to 100 with at most two decimals (0 removes the rollout); add_rule: rollout share of matching customers"`
	RuleID      string            `json:"rule_id,omitempty" jsonschema:"add_rule or remove_rule: rule ID"`
	Customers   []string          `json:"customers,omitempty" jsonschema:"add_rule: platform customer UUIDs"`
	Group       string            `json:"group,omitempty" jsonschema:"add_rule: customer group key"`
	Attributes  []flags.Condition `json:"attributes,omitempty" jsonschema:"add_rule: conditions on gregale.country, gregale.ua_family or app.* attributes; op is in, not_in or ends_with"`
	Value       *string           `json:"value,omitempty" jsonschema:"add_rule: value served, true/false or a variant key; omit to serve true on a boolean flag or weighted variants"`
}

type planFlagChangeArgs struct {
	Project     string       `json:"project" jsonschema:"Project slug"`
	Environment string       `json:"environment" jsonschema:"Named project environment, e.g. production"`
	Changes     []flagChange `json:"changes" jsonschema:"Ordered edits, 1 to 20, applied to the current configuration"`
	Reason      string       `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type planFlagRollbackArgs struct {
	Project     string `json:"project" jsonschema:"Project slug"`
	Environment string `json:"environment" jsonschema:"Named project environment, e.g. production"`
	Version     int64  `json:"version" jsonschema:"Earlier configuration version to restore"`
	Reason      string `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type killFlagArgs struct {
	Project     string `json:"project" jsonschema:"Project slug"`
	Environment string `json:"environment" jsonschema:"Named project environment, e.g. production"`
	Key         string `json:"key" jsonschema:"Flag key to kill"`
	Reason      string `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

// flagDiff is one changed flag; Before or After is nil when it was created
// or deleted.
type flagDiff struct {
	Key    string      `json:"key"`
	Before *flags.Flag `json:"before"`
	After  *flags.Flag `json:"after"`
}

type flagPlan struct {
	ID              string     `json:"plan_id"`
	Action          string     `json:"action"`
	AccountID       string     `json:"account_id"`
	Project         string     `json:"project"`
	ProjectID       string     `json:"project_id"`
	Environment     string     `json:"environment"`
	EnvironmentID   string     `json:"environment_id"`
	Reason          string     `json:"reason"`
	ExpectedVersion int64      `json:"expected_version"`
	RestoreVersion  int64      `json:"restore_version,omitempty"`
	Summaries       []string   `json:"summaries,omitempty"`
	Changed         []flagDiff `json:"changed"`
	TrafficEffect   string     `json:"traffic_effect"`
	ExpiresAt       time.Time  `json:"expires_at"`
	config          flags.Config
	receipt         map[string]any
	attempted       bool
}

// flagSnapshot is the part of GET .../flags the tools use.
type flagSnapshot struct {
	Version int64 `json:"version"`
	flags.Config
}

func registerFlagChanges(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_flags", "List a project environment's feature flags: type, enabled/killed state, default, rules, owner and expiry, with the configuration version and lifecycle report (staleness, evaluation counts). Read-only.", m.listFlags)
	register(s, m, "gregale_plan_flag_change", "Review an ordered list of flag edits (create, set, enable, disable, kill, revive, set_percent, add_rule, remove_rule, delete) against the current configuration. Shows each edit's summary and every changed flag before and after; pins the configuration version for 15 minutes. Changes nothing; apply with gregale_apply_flag_change. Production flag writes by agent connections ask a person first.", m.planFlagChange)
	register(s, m, "gregale_plan_flag_rollback", "Review restoring an earlier flag configuration version as a new version. Shows every flag that would change. Changes nothing; apply with gregale_apply_flag_change.", m.planFlagRollback)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_apply_flag_change", "Publish a reviewed flag change or rollback plan, guarded by the version it read: a configuration changed since the plan is refused as plan_stale, never overwritten. Retry the same plan_id after an uncertain response. If a person must approve, follow the approval recovery and retry the same plan_id.", false, m.applyFlagChange)
		registerTool(s, m, "gregale_kill_flag", "Emergency kill switch: make every evaluation of one flag serve the fallback value in the application's code, on the latest configuration, immediately. No plan and no approval; idempotent. Revive it later with a reviewed gregale_plan_flag_change (op revive).", false, m.killFlag)
	}
}

func validFlagReason(reason string) error {
	if reason == "" || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 280 {
		return invalid("invalid_argument", "reason must contain between 1 and 280 valid Unicode characters")
	}
	return nil
}

func (m *management) readFlags(ctx context.Context, project, environment string) (flagSnapshot, error) {
	raw, err := m.client.ProjectFlags(ctx, project, environment)
	if err != nil {
		return flagSnapshot{}, err
	}
	var snap flagSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return flagSnapshot{}, invalid("invalid_response", "flag configuration could not be read")
	}
	if snap.Flags == nil {
		snap.Flags = []flags.Flag{}
	}
	if snap.Groups == nil {
		snap.Groups = map[string][]string{}
	}
	return snap, nil
}

func (m *management) listFlags(ctx context.Context, a flagScopeArgs) (any, error) {
	if _, _, err := m.environmentAccess(ctx, a.Project, a.Environment, false, false); err != nil {
		return nil, err
	}
	snap, err := m.readFlags(ctx, a.Project, a.Environment)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"project": a.Project, "environment": a.Environment, "version": snap.Version, "items": snap.Flags, "total": len(snap.Flags), "groups": slices.Sorted(maps.Keys(snap.Groups))}
	if lifecycle, err := m.client.ProjectFlagLifecycle(ctx, a.Project, a.Environment); err == nil {
		out["lifecycle"] = lifecycle
	}
	return out, nil
}

// flagEdit turns one requested change into a pkg/flags edit.
func flagEdit(c flagChange) (flags.Edit, error) {
	if !flags.ValidKey(c.Key) {
		return nil, fmt.Errorf("%q is not a valid flag key (lowercase letter, then up to 63 of a-z 0-9 _ -)", c.Key)
	}
	withMeta := func(e flags.Edit) flags.Edit { return flags.EditMeta(e, c.Key, c.Owner, c.Expires) }
	switch c.Op {
	case "create":
		description := ""
		if c.Description != nil {
			description = *c.Description
		}
		return withMeta(flags.EditCreate(c.Key, c.Variants, c.Default, description)), nil
	case "set":
		if c.Default == nil && c.Description == nil && c.Owner == nil && c.Expires == nil {
			return nil, errors.New("set needs at least one of default, description, owner, expires")
		}
		return withMeta(flags.EditSet(c.Key, c.Default, c.Description)), nil
	case "enable", "disable":
		return flags.EditOnOff(c.Key, c.Op == "enable"), nil
	case "kill", "revive":
		return flags.EditKill(c.Key, c.Op == "kill"), nil
	case "set_percent":
		bp, err := flags.ParsePercent(c.Percent)
		if err != nil {
			return nil, err
		}
		return flags.EditPercent(c.Key, bp), nil
	case "add_rule":
		if !flags.ValidKey(c.RuleID) {
			return nil, errors.New("rule_id must be a rule ID (lowercase letter, then up to 63 of a-z 0-9 _ -)")
		}
		rule := flags.Rule{ID: c.RuleID, Customers: c.Customers, Group: c.Group, Attributes: c.Attributes}
		if c.Percent != "" {
			bp, err := flags.ParsePercent(c.Percent)
			if err != nil {
				return nil, err
			}
			rule.Rollout = &bp
		}
		if c.Value != nil {
			rule.Value = *c.Value
		}
		return flags.EditRuleAdd(c.Key, rule), nil
	case "remove_rule":
		if c.RuleID == "" {
			return nil, errors.New("remove_rule needs rule_id")
		}
		return flags.EditRuleRemove(c.Key, c.RuleID), nil
	case "delete":
		return flags.EditRemove(c.Key), nil
	}
	return nil, fmt.Errorf("unknown op %q; use create, set, enable, disable, kill, revive, set_percent, add_rule, remove_rule or delete", c.Op)
}

// cloneFlagConfig deep-copies a configuration through JSON, so edits never
// alias the snapshot the diff compares against.
func cloneFlagConfig(c flags.Config) (flags.Config, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return flags.Config{}, err
	}
	var out flags.Config
	return out, json.Unmarshal(raw, &out)
}

func flagDiffs(before, after flags.Config) []flagDiff {
	index := func(c flags.Config) map[string]*flags.Flag {
		out := map[string]*flags.Flag{}
		for i := range c.Flags {
			out[c.Flags[i].Key] = &c.Flags[i]
		}
		return out
	}
	b, a := index(before), index(after)
	keys := map[string]bool{}
	for k := range b {
		keys[k] = true
	}
	for k := range a {
		keys[k] = true
	}
	var out []flagDiff
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		x, _ := json.Marshal(b[k])
		y, _ := json.Marshal(a[k])
		if !bytes.Equal(x, y) {
			out = append(out, flagDiff{Key: k, Before: b[k], After: a[k]})
		}
	}
	return out
}

func (m *management) newFlagPlan(ctx context.Context, project, environment, reason, action string) (*flagPlan, flagSnapshot, error) {
	if err := validFlagReason(reason); err != nil {
		return nil, flagSnapshot{}, err
	}
	p, e, err := m.environmentAccess(ctx, project, environment, false, false)
	if err != nil {
		return nil, flagSnapshot{}, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, flagSnapshot{}, err
	}
	snap, err := m.readFlags(ctx, project, environment)
	if err != nil {
		return nil, flagSnapshot{}, err
	}
	effect := "Every app of the environment evaluates the new configuration on its next request."
	if e.Protected && environment != "production" {
		return nil, flagSnapshot{}, invalid("approval_required", "protected environments take flag changes from an approved project plan or promotion")
	}
	return &flagPlan{Action: action, AccountID: account.ID, Project: project, ProjectID: p.ID, Environment: environment, EnvironmentID: e.ID, Reason: reason, ExpectedVersion: snap.Version, TrafficEffect: effect, ExpiresAt: time.Now().Add(flagPlanTTL)}, snap, nil
}

func (m *management) planFlagChange(ctx context.Context, a planFlagChangeArgs) (any, error) {
	if len(a.Changes) == 0 || len(a.Changes) > maxFlagChanges {
		return nil, invalid("invalid_argument", fmt.Sprintf("changes must hold 1 to %d edits", maxFlagChanges))
	}
	edits := make([]flags.Edit, 0, len(a.Changes))
	for i, c := range a.Changes {
		e, err := flagEdit(c)
		if err != nil {
			return nil, invalid("invalid_argument", fmt.Sprintf("changes[%d]: %v", i, err))
		}
		edits = append(edits, e)
	}
	p, snap, err := m.newFlagPlan(ctx, a.Project, a.Environment, a.Reason, "flag_change")
	if err != nil {
		return nil, err
	}
	next, err := cloneFlagConfig(snap.Config)
	if err != nil {
		return nil, err
	}
	for i, e := range edits {
		summary, err := e(&next)
		if err != nil {
			return nil, invalid("invalid_argument", fmt.Sprintf("changes[%d]: %v", i, err))
		}
		p.Summaries = append(p.Summaries, summary)
	}
	if err := flags.Validate(next); err != nil {
		return nil, invalid("invalid_argument", "the changed configuration is invalid: "+err.Error())
	}
	p.config = next
	p.Changed = flagDiffs(snap.Config, next)
	if len(p.Changed) == 0 {
		return nil, invalid("no_change", "the edits leave the configuration unchanged: "+fmt.Sprint(p.Summaries))
	}
	return m.saveFlagPlan(p)
}

func (m *management) planFlagRollback(ctx context.Context, a planFlagRollbackArgs) (any, error) {
	if a.Version < 1 {
		return nil, invalid("invalid_argument", "version must be positive")
	}
	p, snap, err := m.newFlagPlan(ctx, a.Project, a.Environment, a.Reason, "flag_rollback")
	if err != nil {
		return nil, err
	}
	if a.Version >= snap.Version {
		return nil, invalid("invalid_argument", fmt.Sprintf("version must be earlier than the current version %d", snap.Version))
	}
	versions, err := m.client.ProjectFlagVersions(ctx, a.Project, a.Environment, a.Version+1)
	if err != nil {
		return nil, err
	}
	var rows []flagSnapshot
	if err := json.Unmarshal(versions, &rows); err != nil {
		return nil, invalid("invalid_response", "flag versions could not be read")
	}
	at := slices.IndexFunc(rows, func(r flagSnapshot) bool { return r.Version == a.Version })
	if at < 0 {
		return nil, invalid("not_found", fmt.Sprintf("flag configuration version %d not found", a.Version))
	}
	p.RestoreVersion = a.Version
	p.config = rows[at].Config
	p.Changed = flagDiffs(snap.Config, rows[at].Config)
	p.Summaries = []string{fmt.Sprintf("Restore version %d as a new version", a.Version)}
	return m.saveFlagPlan(p)
}

func (m *management) saveFlagPlan(p *flagPlan) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, old := range m.flagPlans {
		if now.After(old.ExpiresAt) {
			delete(m.flagPlans, id)
		}
	}
	if len(m.flagPlans) >= maxFlagPlans {
		return nil, invalid("plan_capacity", "connection has too many active flag plans; wait for expiration")
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	p.ID = hex.EncodeToString(id)
	m.flagPlans[p.ID] = p
	if err := m.persistMemoryPlansLocked(); err != nil {
		delete(m.flagPlans, p.ID)
		return nil, err
	}
	return *p, nil
}

func isFlagsConflict(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Code == flagsConflictCode
}

func (m *management) applyFlagChange(ctx context.Context, a applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.flagPlans[a.PlanID]
	if p == nil {
		return nil, invalid("invalid_plan", "unknown or expired flag plan; review a new one")
	}
	if p.receipt != nil {
		return p.receipt, nil
	}
	if time.Now().After(p.ExpiresAt) {
		delete(m.flagPlans, a.PlanID)
		return nil, invalid("plan_expired", "plan expired; review a new one")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID {
		return nil, invalid("account_changed", "account identity changed; create a new connection and plan")
	}
	project, env, err := m.environmentAccess(ctx, p.Project, p.Environment, true, false)
	if err != nil {
		return nil, err
	}
	if project.ID != p.ProjectID || env.ID != p.EnvironmentID {
		return nil, invalid("plan_stale", "project or environment identity changed; review a new plan")
	}
	retry := p.attempted
	p.attempted = true
	// Journal the attempt before the write, so a retry after a restart
	// checks for this plan's own publish instead of refusing a conflict.
	if err := m.persistMemoryPlansLocked(); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if p.Action == "flag_rollback" {
		raw, err = m.client.RollbackProjectFlags(ctx, p.Project, p.Environment, p.ExpectedVersion, p.RestoreVersion)
	} else {
		body, merr := json.Marshal(map[string]any{"expected_version": p.ExpectedVersion, "config": p.config})
		if merr != nil {
			return nil, merr
		}
		raw, err = m.client.PublishProjectFlags(ctx, p.Project, p.Environment, body)
	}
	if err != nil {
		if !isFlagsConflict(err) {
			return nil, err
		}
		// A conflict after an uncertain attempt may be this plan's own
		// publish: the next version holding exactly the planned configuration.
		if retry {
			if current, rerr := m.readFlags(ctx, p.Project, p.Environment); rerr == nil && current.Version == p.ExpectedVersion+1 && sameFlagConfig(current.Config, p.config) {
				return m.flagReceipt(p, current.Version), nil
			}
		}
		return nil, invalid("plan_stale", "the flag configuration changed since this plan; review a new one")
	}
	var published flagSnapshot
	if err := json.Unmarshal(raw, &published); err != nil || published.Version <= p.ExpectedVersion {
		return nil, invalid("invalid_response", "published flag configuration version is missing")
	}
	return m.flagReceipt(p, published.Version), nil
}

func sameFlagConfig(a, b flags.Config) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

func (m *management) flagReceipt(p *flagPlan, version int64) map[string]any {
	p.receipt = map[string]any{"plan_id": p.ID, "action": p.Action, "project": p.Project, "environment": p.Environment, "result": map[string]any{"version": version, "previous_version": p.ExpectedVersion, "summaries": p.Summaries}}
	// The write succeeded; a failed journal only costs the replayed receipt.
	_ = m.persistMemoryPlansLocked()
	return p.receipt
}

func (m *management) killFlag(ctx context.Context, a killFlagArgs) (any, error) {
	if err := validFlagReason(a.Reason); err != nil {
		return nil, err
	}
	if !flags.ValidKey(a.Key) {
		return nil, invalid("invalid_argument", "key is not a valid flag key")
	}
	if _, _, err := m.environmentAccess(ctx, a.Project, a.Environment, true, false); err != nil {
		return nil, err
	}
	out, err := m.client.KillProjectFlag(ctx, a.Project, a.Environment, a.Key)
	if err != nil {
		return nil, err
	}
	if out.Key != a.Key || !out.Killed || out.Version < 1 {
		return nil, invalid("invalid_response", "kill receipt does not name the flag")
	}
	summary := fmt.Sprintf("Killed %s: requests now get the fallback value in your code", a.Key)
	if !out.Changed {
		summary = a.Key + " was already killed"
	}
	return map[string]any{"project": a.Project, "environment": a.Environment, "key": out.Key, "killed": true, "changed": out.Changed, "version": out.Version, "summary": summary}, nil
}
