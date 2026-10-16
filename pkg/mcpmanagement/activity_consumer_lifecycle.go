package mcpmanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const listActivityConsumersTool = "gregale_list_environment_cache_activity_consumers"
const retireActivityConsumerTool = "gregale_retire_environment_cache_activity_consumer"

type activityConsumerListArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Owning project"`
	Environment string `json:"environment" jsonschema:"Named environment with current read consent"`
	After       string `json:"after,omitempty" jsonschema:"Opaque list continuation from this connection and target; restart after retirement"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Page size, default 20 and maximum 50"`
}

type activityConsumerRetireArgs struct {
	App                string `json:"app" jsonschema:"Gregale app slug"`
	Project            string `json:"project" jsonschema:"Owning project"`
	Environment        string `json:"environment" jsonschema:"Named environment with current read consent"`
	Consumer           string `json:"consumer" jsonschema:"Exact consumer to retire; removes only its bridge checkpoint"`
	ExpectedRevision   int64  `json:"expected_revision" jsonschema:"Exact current revision of the consumer being retired"`
	ExpectedGeneration string `json:"expected_generation,omitempty" jsonschema:"Generation from the latest get/list result; retirement rotates it for the whole connection"`
}

type activityConsumerListCursor struct {
	Version    int    `json:"v"`
	Binding    string `json:"b"`
	Generation string `json:"g"`
	After      string `json:"a"`
}

type activityConsumerListEntry struct {
	Consumer  string    `json:"consumer"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

type activityConsumerListResult struct {
	AppID         string                      `json:"app_id"`
	ProjectID     string                      `json:"project_id"`
	EnvironmentID string                      `json:"environment_id"`
	Scope         string                      `json:"scope"`
	Generation    string                      `json:"generation"`
	Entries       []activityConsumerListEntry `json:"entries"`
	NextCursor    string                      `json:"next_cursor,omitempty"`
	Basis         string                      `json:"basis"`
}

type activityConsumerRetireResult struct {
	Consumer        string `json:"consumer"`
	AppID           string `json:"app_id"`
	ProjectID       string `json:"project_id"`
	EnvironmentID   string `json:"environment_id"`
	Scope           string `json:"scope"`
	Status          string `json:"status"`
	RetiredRevision int64  `json:"retired_revision"`
	Generation      string `json:"generation"`
}

func (m *management) consumerListBinding(b activityConsumerBinding) string {
	b.Consumer = ""
	raw, _ := json.Marshal([]string{m.opts.planNamespace, b.key()})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func decodeConsumerListCursor(raw string) (activityConsumerListCursor, error) {
	var c activityConsumerListCursor
	bad := invalid("invalid_argument", "invalid consumer list continuation; restart listing without after")
	if len(raw) > api.MCPActivityConsumerListCursorMaxBytes {
		return c, bad
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return c, bad
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(&struct{}{}) != io.EOF || c.Version != 1 || len(c.Binding) != 64 || len(c.After) > api.MCPActivityConsumerNameMaxBytes || !activityConsumerName.MatchString(c.After) || c.Generation != "" && !consumerGeneration.MatchString(c.Generation) {
		return c, bad
	}
	return c, nil
}

func (m *management) listActivityConsumers(ctx context.Context, args activityConsumerListArgs) (any, error) {
	limit := args.Limit
	if limit == 0 {
		limit = api.MCPActivityConsumerListDefault
	}
	if limit < 1 || limit > api.MCPActivityConsumerListMax || len(args.After) > api.MCPActivityConsumerListCursorMaxBytes {
		return nil, invalid("invalid_argument", "select a consumer page between 1 and 50 entries")
	}
	var continuation activityConsumerListCursor
	var err error
	if args.After != "" {
		continuation, err = decodeConsumerListCursor(args.After)
		if err != nil {
			return nil, err
		}
	}
	// The sentinel name is used for target validation only; no record is created.
	base := activityConsumerArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: "list"}
	b, point, err := m.activityConsumerTarget(ctx, base)
	if err != nil {
		return nil, err
	}
	if err = m.validateConsumerCursor(ctx, base, b, point.RestartCursor); err != nil {
		return nil, err
	}
	binding := m.consumerListBinding(b)
	if args.After != "" && continuation.Binding != binding {
		return nil, invalid("invalid_argument", "list continuation belongs to another connection or immutable target")
	}
	s := m.opts.planStore
	s.mu.Lock()
	defer s.mu.Unlock()
	records, epoch, err := m.consumerStateLocked(ctx, base, b)
	if err != nil {
		return nil, err
	}
	if args.After != "" && continuation.Generation != epoch.Generation {
		return nil, consumerGenerationConflict()
	}
	snapshot, err := scopedConsumerSnapshot(records, epoch, b)
	if err != nil {
		return nil, err
	}
	out := activityConsumerListResult{AppID: b.App, ProjectID: b.Project, EnvironmentID: b.Environment, Scope: b.Scope, Generation: epoch.Generation, Entries: []activityConsumerListEntry{}, Basis: "Live name-ordered metadata, not a frozen snapshot or current cursor readiness. Get an exact consumer to validate expiry; retirement invalidates continuations."}
	for _, record := range snapshot.records {
		if record.Binding.Consumer > continuation.After {
			out.Entries = append(out.Entries, activityConsumerListEntry{Consumer: record.Binding.Consumer, Revision: record.Revision, UpdatedAt: record.UpdatedAt})
		}
	}
	if len(out.Entries) > limit {
		out.Entries = out.Entries[:limit]
		raw, _ := json.Marshal(activityConsumerListCursor{Version: 1, Binding: binding, Generation: epoch.Generation, After: out.Entries[limit-1].Consumer})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	outGuard := guardConsumerRead(out, base, b, activityConsumerRecord{})
	outGuard.snapshot = &snapshot
	return outGuard, nil
}

func (m *management) retireActivityConsumer(ctx context.Context, args activityConsumerRetireArgs) (any, error) {
	if args.ExpectedRevision < 1 || args.ExpectedRevision > api.RouteRequirementsMaxRevision || len(args.ExpectedGeneration) > api.MCPActivityConsumerGenerationMaxBytes {
		return nil, invalid("invalid_argument", "retire an existing consumer with its exact revision and generation")
	}
	base := activityConsumerArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: args.Consumer}
	b, point, err := m.activityConsumerTarget(ctx, base)
	if err != nil {
		return nil, err
	}
	// Retirement must remain possible when the saved cursor has expired.
	if err = m.validateConsumerCursor(ctx, base, b, point.RestartCursor); err != nil {
		return nil, err
	}
	if err = m.authorizeArgs(ctx, base); err != nil {
		return nil, err
	}
	return m.retireConsumerRecord(ctx, base, b, args)
}

func (m *management) retireConsumerRecord(ctx context.Context, base activityConsumerArgs, b activityConsumerBinding, args activityConsumerRetireArgs) (any, error) {
	s := m.opts.planStore
	s.mu.Lock()
	defer s.mu.Unlock()
	records, epoch, err := m.consumerStateLocked(ctx, base, b)
	if err != nil {
		return nil, err
	}
	key := b.key()
	record, exists := records[key]
	result := func() activityConsumerRetireResult {
		return activityConsumerRetireResult{Consumer: b.Consumer, AppID: b.App, ProjectID: b.Project, EnvironmentID: b.Environment, Scope: b.Scope, Status: "retired", RetiredRevision: args.ExpectedRevision, Generation: epoch.Generation}
	}
	if args.ExpectedGeneration != epoch.Generation {
		if !exists && epoch.Previous == args.ExpectedGeneration && epoch.RetiredKey == key && epoch.RetiredRevision == args.ExpectedRevision {
			return result(), nil
		}
		return nil, consumerGenerationConflict()
	}
	if !exists {
		return nil, invalid("consumer_not_found", "consumer has no saved checkpoint to retire")
	}
	if record.Revision != args.ExpectedRevision {
		return nil, invalid("checkpoint_conflict", "consumer revision changed; read it again before retiring")
	}
	generation, err := randomToken("cg_")
	if err != nil {
		return nil, invalid("request_failed", "consumer generation could not be created")
	}
	epoch = activityConsumerEpoch{Generation: generation, Previous: epoch.Generation, RetiredKey: key, RetiredRevision: record.Revision}
	delete(records, key)
	if err = m.commitConsumerStateLocked(ctx, records, &epoch); err != nil {
		return nil, err
	}
	return result(), nil
}
