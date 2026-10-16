package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const getActivityConsumerTool = "gregale_get_environment_cache_activity_consumer"
const ackActivityConsumerTool = "gregale_ack_environment_cache_activity_consumer"

type activityConsumerArgs struct {
	App         string `json:"app" jsonschema:"Gregale app slug"`
	Project     string `json:"project" jsonschema:"Owning project"`
	Environment string `json:"environment" jsonschema:"Named environment with current read consent"`
	Consumer    string `json:"consumer" jsonschema:"Consumer name: lowercase letter followed by lowercase letters, digits, underscores or hyphens; maximum 64 bytes"`
}

type activityConsumerAckArgs struct {
	App                string `json:"app" jsonschema:"Gregale app slug"`
	Project            string `json:"project" jsonschema:"Owning project"`
	Environment        string `json:"environment" jsonschema:"Named environment with current read consent"`
	Consumer           string `json:"consumer" jsonschema:"Consumer name, maximum 64 bytes"`
	Cursor             string `json:"cursor" jsonschema:"Exact activity cursor after processing a page or completing a rebuilt baseline; never advance before processing"`
	ExpectedRevision   int64  `json:"expected_revision" jsonschema:"Last consumer revision from get or acknowledgment; zero creates a consumer"`
	ExpectedGeneration string `json:"expected_generation,omitempty" jsonschema:"Connection generation from the latest consumer get/list result; required after any consumer retirement"`
	BaselineRebuilt    bool   `json:"baseline_rebuilt,omitempty" jsonschema:"Explicitly confirm a completed current baseline before replacing an expired checkpoint"`
}

type activityConsumerBinding struct {
	Endpoint, Account, App, Project, Environment, Scope, Consumer string
}

func (b activityConsumerBinding) key() string {
	data, _ := json.Marshal(b)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

type activityConsumerRecord struct {
	Generation string `json:"-"`
	Binding    activityConsumerBinding
	Cursor     string
	Revision   int64
	UpdatedAt  time.Time
}

type activityConsumerResult struct {
	Lag              *activityConsumerLag                  `json:"lag,omitempty"`
	Generation       string                                `json:"generation"`
	Consumer         string                                `json:"consumer"`
	AppID            string                                `json:"app_id"`
	ProjectID        string                                `json:"project_id"`
	EnvironmentID    string                                `json:"environment_id"`
	Scope            string                                `json:"scope"`
	Revision         int64                                 `json:"revision"`
	Cursor           string                                `json:"cursor,omitempty"`
	Status           string                                `json:"status"`
	UpdatedAt        *time.Time                            `json:"updated_at,omitempty"`
	ActivityRecovery *api.EnvironmentCacheActivityRecovery `json:"activity_recovery,omitempty"`
	Hint             string                                `json:"hint,omitempty"`
}

func registerActivityConsumers(s *mcp.Server, m *management) {
	register(s, m, activityConsumerHealthTool, "Read a bounded page of named consumer health and full-scope summary counts. Uses shared validated feed bounds to report readiness, retained backlog, expired-history gaps and ACK age. Requires current read consent and encrypted state; no saved or recovery cursors, event pages or ACK arguments. Continuations bind the scoped saved records and connection generation; restart if they change. Feed observations remain live, not a frozen snapshot. Never saves progress or starts a watch.", m.getActivityConsumerHealth)
	register(s, m, recoverActivityConsumerTool, "Recover one bounded baseline page for a named consumer without changing progress. Continuations bind the consumer, connection, immutable target, revision and generation. Process every page, then explicitly submit acknowledgment arguments returned only with complete=true. Conflicts or retention expiry require discarding partial results and restarting without recovery_token. Tokens are pagination data, not proof of processing.", m.recoverActivityConsumer)
	register(s, m, watchActivityConsumerTool, "Wait for one bounded activity page after a named consumer's saved cursor. Revalidates read consent and saved revision/generation on each observation. Returns page and acknowledgment arguments; explicitly ACK only after processing. Uninitialized or expired consumers require baseline recovery. Never saves progress. Top-level cursor is saved progress; page.cursor is the candidate acknowledgment.", m.watchActivityConsumer)
	register(s, m, listActivityConsumersTool, "List one bounded page of saved consumer names, revisions and update times for the selected immutable environment only. Requires current read consent and encrypted state. No cursor or expiry validation per entry; get a consumer for current readiness. Live name-sorted pages; retirement invalidates continuations.", m.listActivityConsumers)
	registerToolAnnotations(s, m, retireActivityConsumerTool, "Explicitly retire one durable consumer with its exact revision and connection generation, freeing its checkpoint capacity. Deletes bridge progress only and rotates the generation for the whole connection; get fresh generations before further acknowledgments. No native writes or reversal of downstream work. Exact last-retirement retry cannot retire a recreated consumer using an old generation.", false, false, true, m.retireActivityConsumer)
	register(s, m, getActivityConsumerTool, "Read a durable named activity checkpoint from the encrypted bridge journal. Revalidates current environment read consent and feed access. Uninitialized consumers require an explicit initial acknowledgment; expired checkpoints require a rebuilt baseline. Includes point-in-time retained backlog, expired history gap and acknowledgment age. Does not consume events.", m.getActivityConsumer)
	registerToolEffect(s, m, ackActivityConsumerTool, "Explicitly acknowledge processing through an activity cursor in the encrypted bridge journal. Requires current read consent, durable state, and expected_revision; no platform writes. Confirm baseline_rebuilt only after completing baseline recovery if the old cursor expired. Retry the exact cursor and revision after reply loss. Does not prove downstream effects occurred.", false, false, m.ackActivityConsumer)
}

var activityConsumerName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func (m *management) activityConsumerTarget(ctx context.Context, args activityConsumerArgs) (activityConsumerBinding, api.EnvironmentCacheActivityRecovery, error) {
	var b activityConsumerBinding
	var point api.EnvironmentCacheActivityRecovery
	if len(args.Consumer) > api.MCPActivityConsumerNameMaxBytes || !activityConsumerName.MatchString(args.Consumer) || args.Environment == "" {
		return b, point, invalid("invalid_argument", "select a named environment and consumer name of at most 64 lowercase letters, digits, underscores or hyphens, starting with a letter")
	}
	if m.opts.planStore == nil || m.opts.planNamespace == "" {
		return b, point, invalid("durable_state_required", "Configure the encrypted MCP state file and independent key before using named consumers")
	}
	if err := m.requireScheduleRecoverySupport(ctx); err != nil {
		return b, point, err
	}
	var err error
	point, err = m.scheduleActivityCheckpoint(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment})
	if err != nil {
		return b, point, scheduleWorkflowError(err)
	}
	// The checkpoint's cursor binds the account; validate it again when using a
	// saved cursor. Identity changes during any subsequent feed read fail closed.
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return b, point, err
	}
	if err = validateScheduleActivityCheckpoint(point, account.ID, point.AppID, point.ProjectID, point.EnvironmentID, args.Environment); err != nil {
		return b, point, err
	}
	b = activityConsumerBinding{Endpoint: strings.TrimRight(m.opts.APIBase, "/"), Account: account.ID, App: point.AppID, Project: point.ProjectID, Environment: point.EnvironmentID, Scope: args.Environment, Consumer: args.Consumer}
	return b, point, nil
}

func (m *management) validateConsumerCursor(ctx context.Context, args activityConsumerArgs, b activityConsumerBinding, cursor string) error {
	if _, err := api.ParseEnvironmentCacheActivityCursor(cursor, b.Account, b.App, b.Project, b.Environment); err != nil || cursor == "" {
		return invalid("invalid_argument", "consumer cursor must belong to the selected account and immutable environment")
	}
	value, err := m.getEnvironmentCacheScheduleActivity(ctx, environmentScheduleActivityArgs{App: args.App, Project: args.Project, Environment: args.Environment, After: cursor, Limit: 1})
	if err != nil {
		return scheduleWorkflowError(err)
	}
	page := value.(api.EnvironmentCacheScheduleActivity)
	if page.AppID != b.App || page.ProjectID != b.Project || page.EnvironmentID != b.Environment {
		return invalid("plan_stale", "consumer target changed during validation")
	}
	if _, err = api.ParseEnvironmentCacheActivityCursor(page.Cursor, b.Account, b.App, b.Project, b.Environment); err != nil {
		return invalid("plan_stale", "consumer account changed during validation")
	}
	return nil
}

func consumerResult(b activityConsumerBinding, record activityConsumerRecord) activityConsumerResult {
	out := activityConsumerResult{Consumer: b.Consumer, AppID: b.App, ProjectID: b.Project, EnvironmentID: b.Environment, Scope: b.Scope, Revision: record.Revision, Cursor: record.Cursor, Status: "ready"}
	out.Generation = record.Generation
	if record.Revision == 0 {
		out.Status = "uninitialized"
	} else {
		out.UpdatedAt = &record.UpdatedAt
	}
	return out
}

func (m *management) getActivityConsumer(ctx context.Context, args activityConsumerArgs) (any, error) {
	b, _, err := m.activityConsumerTarget(ctx, args)
	if err != nil {
		return nil, err
	}
	value, err := m.inspectConsumerAtTarget(ctx, args, b)
	if err != nil {
		return nil, err
	}
	out := value.(activityConsumerResult)
	guarded := guardConsumerRead(out, args, b, activityConsumerRecord{Revision: out.Revision, Generation: out.Generation})
	guarded.sample = &out
	return guarded, nil
}

// Shared read validation for tools and prompts. Only exact get-tool calls sample
// consumer lag metrics; prompt retrieval never creates or advances progress.
func (m *management) inspectActivityConsumer(ctx context.Context, args activityConsumerArgs) (any, error) {
	b, _, err := m.activityConsumerTarget(ctx, args)
	if err != nil {
		return nil, err
	}
	return m.inspectConsumerAtTarget(ctx, args, b)
}

func (m *management) inspectConsumerAtTarget(ctx context.Context, args activityConsumerArgs, b activityConsumerBinding) (any, error) {
	record, err := m.consumerRecord(ctx, args, b, nil)
	if err != nil {
		return nil, err
	}
	// Capture bounds AFTER reading the saved record. An acknowledgment committed
	// after target discovery may already be ahead of its initial checkpoint.
	point, err := m.scheduleActivityCheckpoint(ctx, appArgs{App: args.App, Project: args.Project, Environment: args.Environment})
	if err != nil {
		return nil, scheduleWorkflowError(err)
	}
	if err = validateScheduleActivityCheckpoint(point, b.Account, b.App, b.Project, b.Environment, b.Scope); err != nil {
		return nil, err
	}
	observedAt := time.Now().UTC()
	cursor := record.Cursor
	if record.Revision == 0 {
		cursor = point.RestartCursor
	}
	err = m.validateConsumerCursor(ctx, args, b, cursor)
	out := consumerResult(b, record)
	var expired *activityCursorExpiredError
	if record.Revision > 0 && errors.As(err, &expired) {
		point, observedAt = expired.recovery, time.Now().UTC()
		out.Status, out.ActivityRecovery = "cursor_expired", &expired.recovery
		out.Hint = "Rebuild the complete current baseline using gregale_recover_environment_cache_activity_consumer, then explicitly submit its final acknowledgment arguments after processing every page. Historical events cannot be reconstructed."
	} else if err != nil {
		return nil, err
	}
	// Recheck current grant/store after native reads, including revocation while
	// in flight. Never serve an old namespace from an in-memory manager cache.
	fresh, err := m.consumerRecord(ctx, args, b, nil)
	if err != nil {
		return nil, err
	}
	if fresh.Generation != record.Generation {
		return nil, consumerGenerationConflict()
	}
	if fresh.Revision != record.Revision {
		return nil, invalid("checkpoint_conflict", "consumer revision changed during inspection; read it again")
	}
	out.Lag, err = consumerLag(b, record, point, observedAt)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (m *management) ackActivityConsumer(ctx context.Context, args activityConsumerAckArgs) (any, error) {
	if args.ExpectedRevision < 0 || args.ExpectedRevision >= api.RouteRequirementsMaxRevision || len(args.ExpectedGeneration) > api.MCPActivityConsumerGenerationMaxBytes {
		return nil, invalid("invalid_argument", "expected_revision is outside the supported range")
	}
	base := activityConsumerArgs{App: args.App, Project: args.Project, Environment: args.Environment, Consumer: args.Consumer}
	b, _, err := m.activityConsumerTarget(ctx, base)
	if err != nil {
		return nil, err
	}
	before, err := m.consumerRecord(ctx, base, b, nil)
	if err != nil {
		return nil, err
	}
	if before.Generation != args.ExpectedGeneration {
		return nil, consumerGenerationConflict()
	}
	if before.Revision != args.ExpectedRevision && !(before.Revision == args.ExpectedRevision+1 && before.Cursor == args.Cursor) {
		return nil, invalid("checkpoint_conflict", "consumer revision changed; read it again before acknowledging")
	}
	if err = m.validateConsumerCursor(ctx, base, b, args.Cursor); err != nil {
		return nil, err
	}
	if before.Revision > 0 {
		old, _ := api.ParseEnvironmentCacheActivityCursor(before.Cursor, b.Account, b.App, b.Project, b.Environment)
		next, _ := api.ParseEnvironmentCacheActivityCursor(args.Cursor, b.Account, b.App, b.Project, b.Environment)
		if next.Sequence < old.Sequence {
			return nil, invalid("checkpoint_regression", "acknowledgment cannot move a consumer backwards")
		}
		if err = m.validateConsumerCursor(ctx, base, b, before.Cursor); err != nil {
			var expired *activityCursorExpiredError
			if !args.BaselineRebuilt || !errors.As(err, &expired) {
				return nil, err
			}
		}
	}
	if err = m.authorizeArgs(ctx, base); err != nil {
		return nil, err
	}
	record, err := m.consumerRecord(ctx, base, b, &args)
	if err != nil {
		return nil, err
	}
	return consumerResult(b, record), nil
}
