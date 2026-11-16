package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIPromptStore = (*PgStore)(nil)

// outboundAIPromptColumns are read from `outbound_ai_prompts prompt LEFT
// JOIN outbound_ai_prompt_canaries canary ON canary.prompt_id = prompt.id`.
const outboundAIPromptColumns = `prompt.id, prompt.account_id, prompt.name, prompt.description, prompt.live_version,
       prompt.previous_live_version, prompt.latest_version, prompt.created_at, prompt.updated_at,
       canary.version, canary.percent, canary.max_error_rate_increase, canary.max_latency_increase_percent,
       canary.min_calls, canary.window_minutes, canary.state, canary.reason, canary.started_at, canary.decided_at`

const outboundAIPromptFrom = `outbound_ai_prompts prompt
  LEFT JOIN outbound_ai_prompt_canaries canary ON canary.prompt_id = prompt.id`

func scanOutboundAIPrompt(row pgx.Row) (OutboundAIPrompt, error) {
	var prompt OutboundAIPrompt
	var id, accountID pgtype.UUID
	var canary outboundAIRouteCanaryScan
	if err := row.Scan(append([]any{&id, &accountID, &prompt.Name, &prompt.Description, &prompt.LiveVersion,
		&prompt.PreviousLiveVersion, &prompt.LatestVersion, &prompt.CreatedAt, &prompt.UpdatedAt}, canary.dest()...)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OutboundAIPrompt{}, ErrNotFound
		}
		return OutboundAIPrompt{}, mapErr(err)
	}
	prompt.ID, prompt.AccountID = pgUUIDString(id), pgUUIDString(accountID)
	prompt.CreatedAt, prompt.UpdatedAt = prompt.CreatedAt.UTC(), prompt.UpdatedAt.UTC()
	if route := canary.canary(); route != nil {
		prompt.Canary = &OutboundAIPromptCanary{Version: route.Position, Percent: route.Percent,
			MaxErrorRateIncrease: route.MaxErrorRateIncrease, MaxLatencyIncreasePercent: route.MaxLatencyIncreasePercent,
			MinCalls: route.MinCalls, WindowMinutes: route.WindowMinutes, State: route.State, Reason: route.Reason,
			StartedAt: route.StartedAt, DecidedAt: route.DecidedAt}
	}
	return prompt, nil
}

// lockOutboundAIPrompt reads an account's prompt by name and locks it.
func lockOutboundAIPrompt(ctx context.Context, tx pgx.Tx, accountID, name string) (OutboundAIPrompt, error) {
	return scanOutboundAIPrompt(tx.QueryRow(ctx, `SELECT `+outboundAIPromptColumns+` FROM `+outboundAIPromptFrom+`
		 WHERE prompt.account_id = $1 AND prompt.name = $2 FOR UPDATE OF prompt`, mustPgUUID(accountID), name))
}

func outboundAIPromptHasVersion(ctx context.Context, tx pgx.Tx, promptID string, version int) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outbound_ai_prompt_versions WHERE prompt_id = $1 AND version = $2)`,
		mustPgUUID(promptID), version).Scan(&exists)
	return exists, mapErr(err)
}

func (s *PgStore) ListOutboundAIPrompts(ctx context.Context, accountID string) ([]OutboundAIPrompt, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+outboundAIPromptColumns+` FROM `+outboundAIPromptFrom+`
		 WHERE prompt.account_id = $1 ORDER BY prompt.name`, mustPgUUID(accountID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]OutboundAIPrompt, 0)
	for rows.Next() {
		prompt, err := scanOutboundAIPrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, prompt)
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) GetOutboundAIPrompt(ctx context.Context, accountID, name string) (OutboundAIPrompt, error) {
	return scanOutboundAIPrompt(s.pool.QueryRow(ctx, `SELECT `+outboundAIPromptColumns+` FROM `+outboundAIPromptFrom+`
		 WHERE prompt.account_id = $1 AND prompt.name = $2`, mustPgUUID(accountID), name))
}

// outboundAIPromptTargetAllowed checks a version's target against the
// account's OpenAI-format routes and enabled managed AI integrations.
func outboundAIPromptTargetAllowed(ctx context.Context, tx pgx.Tx, accountID string, target api.OutboundAIPromptTarget) (bool, error) {
	if target.RouteID != "" {
		var format string
		err := tx.QueryRow(ctx, `SELECT format FROM outbound_ai_routes WHERE id = $1 AND account_id = $2 FOR SHARE`,
			mustPgUUID(target.RouteID), mustPgUUID(accountID)).Scan(&format)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return format == api.OutboundAIRouteFormatOpenAI, mapErr(err)
	}
	targets := []OutboundAIRouteTarget{{IntegrationID: target.IntegrationID, Model: target.Model}}
	providers, err := outboundAIProviders(ctx, tx, accountID, targets)
	if err != nil {
		return false, err
	}
	return outboundAIRouteTargetsAllowed(api.OutboundAIRouteFormatOpenAI, targets, providers), nil
}

const outboundAIPromptVersionColumns = `prompt_id, version, messages, defaults, params, route_id, integration_id,
       COALESCE(model, ''), note, created_at`

func scanOutboundAIPromptVersion(row pgx.Row) (OutboundAIPromptVersion, error) {
	var version OutboundAIPromptVersion
	var promptID, routeID, integrationID pgtype.UUID
	var messages, defaults, params []byte
	if err := row.Scan(&promptID, &version.Version, &messages, &defaults, &params, &routeID, &integrationID,
		&version.Target.Model, &version.Note, &version.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OutboundAIPromptVersion{}, ErrNotFound
		}
		return OutboundAIPromptVersion{}, mapErr(err)
	}
	version.PromptID, version.CreatedAt = pgUUIDString(promptID), version.CreatedAt.UTC()
	if routeID.Valid {
		version.Target.RouteID = pgUUIDString(routeID)
	}
	if integrationID.Valid {
		version.Target.IntegrationID = pgUUIDString(integrationID)
	}
	if err := json.Unmarshal(messages, &version.Messages); err != nil {
		return OutboundAIPromptVersion{}, err
	}
	if err := json.Unmarshal(defaults, &version.Defaults); err != nil {
		return OutboundAIPromptVersion{}, err
	}
	if len(version.Defaults) == 0 {
		version.Defaults = nil
	}
	if err := json.Unmarshal(params, &version.Params); err != nil {
		return OutboundAIPromptVersion{}, err
	}
	return version, nil
}

func nullablePgUUID(id string) pgtype.UUID {
	if id == "" {
		return pgtype.UUID{}
	}
	return mustPgUUID(id)
}

func nullableText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// PushOutboundAIPromptVersion stores a version under the account lock, so
// the prompt limit holds against concurrent pushes.
func (s *PgStore) PushOutboundAIPromptVersion(ctx context.Context, push OutboundAIPromptPush) (OutboundAIPrompt, OutboundAIPromptVersion, bool, error) {
	if !validOutboundAIPromptVersion(push) {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE id = $1 FOR UPDATE`, mustPgUUID(push.AccountID)).Scan(&locked); err != nil {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, mapErr(err)
	}
	allowed, err := outboundAIPromptTargetAllowed(ctx, tx, push.AccountID, push.Version.Target)
	if err != nil {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, err
	}
	if !allowed {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, ErrInvalidArgument
	}
	prompt, err := lockOutboundAIPrompt(ctx, tx, push.AccountID, push.Name)
	if errors.Is(err, ErrNotFound) {
		prompt, err = createOutboundAIPrompt(ctx, tx, push)
	}
	if err != nil {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, err
	}
	if prompt.LatestVersion > 0 {
		latest, err := scanOutboundAIPromptVersion(tx.QueryRow(ctx, `SELECT `+outboundAIPromptVersionColumns+`
			  FROM outbound_ai_prompt_versions WHERE prompt_id = $1 AND version = $2`, mustPgUUID(prompt.ID), prompt.LatestVersion))
		if err == nil && api.SameOutboundAIPromptVersion(outboundAIPromptPushRequest(latest), outboundAIPromptPushRequest(push.Version)) {
			return prompt, latest, false, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, err
		}
	}
	version, err := insertOutboundAIPromptVersion(ctx, tx, prompt, push.Version)
	if err != nil {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, err
	}
	live := prompt.LiveVersion
	if live == nil || push.Live {
		live = promptVersionPtr(version.Version)
	}
	prompt, err = updateOutboundAIPromptPointers(ctx, tx, prompt, live, version.Version, push.Description)
	if err != nil {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, err
	}
	if err := pruneOutboundAIPromptVersions(ctx, tx, prompt); err != nil {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboundAIPrompt{}, OutboundAIPromptVersion{}, false, mapErr(err)
	}
	return prompt, version, true, nil
}

func createOutboundAIPrompt(ctx context.Context, tx pgx.Tx, push OutboundAIPromptPush) (OutboundAIPrompt, error) {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM outbound_ai_prompts WHERE account_id = $1`, mustPgUUID(push.AccountID)).Scan(&count); err != nil {
		return OutboundAIPrompt{}, mapErr(err)
	}
	if count >= push.MaxPrompts {
		return OutboundAIPrompt{}, ErrOutboundAIPromptLimit
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbound_ai_prompts (id, account_id, name) VALUES ($1, $2, $3)`,
		mustPgUUID(push.ID), mustPgUUID(push.AccountID), push.Name); err != nil {
		return OutboundAIPrompt{}, mapErr(err)
	}
	return lockOutboundAIPrompt(ctx, tx, push.AccountID, push.Name)
}

func insertOutboundAIPromptVersion(ctx context.Context, tx pgx.Tx, prompt OutboundAIPrompt, version OutboundAIPromptVersion) (OutboundAIPromptVersion, error) {
	messages, err := json.Marshal(version.Messages)
	if err != nil {
		return OutboundAIPromptVersion{}, err
	}
	defaults := version.Defaults
	if defaults == nil {
		defaults = map[string]string{}
	}
	encodedDefaults, err := json.Marshal(defaults)
	if err != nil {
		return OutboundAIPromptVersion{}, err
	}
	params, err := json.Marshal(version.Params)
	if err != nil {
		return OutboundAIPromptVersion{}, err
	}
	return scanOutboundAIPromptVersion(tx.QueryRow(ctx, `
		INSERT INTO outbound_ai_prompt_versions (prompt_id, version, messages, defaults, params, route_id, integration_id, model, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+outboundAIPromptVersionColumns, mustPgUUID(prompt.ID), prompt.LatestVersion+1, messages, encodedDefaults, params,
		nullablePgUUID(version.Target.RouteID), nullablePgUUID(version.Target.IntegrationID), nullableText(version.Target.Model), version.Note))
}

// updateOutboundAIPromptPointers sets the live and latest versions and the
// description. A new live version keeps the old one as previous and ends
// any canary.
func updateOutboundAIPromptPointers(ctx context.Context, tx pgx.Tx, prompt OutboundAIPrompt, live *int, latest int, description *string) (OutboundAIPrompt, error) {
	previous := prompt.PreviousLiveVersion
	if live != nil && prompt.LiveVersion != nil && *prompt.LiveVersion != *live {
		previous = promptVersionPtr(*prompt.LiveVersion)
	}
	if live != nil && (prompt.LiveVersion == nil || *prompt.LiveVersion != *live) {
		if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_prompt_canaries WHERE prompt_id = $1`, mustPgUUID(prompt.ID)); err != nil {
			return OutboundAIPrompt{}, mapErr(err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE outbound_ai_prompts
		   SET live_version = $2, previous_live_version = $3, latest_version = $4,
		       description = COALESCE($5, description), updated_at = now()
		 WHERE id = $1`, mustPgUUID(prompt.ID), live, previous, latest, description); err != nil {
		return OutboundAIPrompt{}, mapErr(err)
	}
	return lockOutboundAIPrompt(ctx, tx, prompt.AccountID, prompt.Name)
}

// pruneOutboundAIPromptVersions deletes the oldest version beyond the limit
// that is neither live, previous live nor the canary's.
func pruneOutboundAIPromptVersions(ctx context.Context, tx pgx.Tx, prompt OutboundAIPrompt) error {
	rows, err := tx.Query(ctx, `SELECT version FROM outbound_ai_prompt_versions WHERE prompt_id = $1 ORDER BY version`, mustPgUUID(prompt.ID))
	if err != nil {
		return mapErr(err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return mapErr(err)
	}
	prune := outboundAIPromptPrunable(versions, prompt)
	if prune == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `DELETE FROM outbound_ai_prompt_versions WHERE prompt_id = $1 AND version = $2`, mustPgUUID(prompt.ID), prune)
	return mapErr(err)
}

func (s *PgStore) ListOutboundAIPromptVersions(ctx context.Context, accountID, name string) ([]OutboundAIPromptVersion, error) {
	prompt, err := s.GetOutboundAIPrompt(ctx, accountID, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT prompt_id, version, '[]'::jsonb, '{}'::jsonb, '{}'::jsonb, route_id, integration_id, COALESCE(model, ''), note, created_at
		  FROM outbound_ai_prompt_versions WHERE prompt_id = $1 ORDER BY version DESC`, mustPgUUID(prompt.ID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]OutboundAIPromptVersion, 0)
	for rows.Next() {
		version, err := scanOutboundAIPromptVersion(rows)
		if err != nil {
			return nil, err
		}
		version.Messages = nil
		out = append(out, version)
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) GetOutboundAIPromptVersion(ctx context.Context, accountID, name string, version int) (OutboundAIPromptVersion, error) {
	return scanOutboundAIPromptVersion(s.pool.QueryRow(ctx, `
		SELECT version.prompt_id, version.version, version.messages, version.defaults, version.params, version.route_id,
		       version.integration_id, COALESCE(version.model, ''), version.note, version.created_at
		  FROM outbound_ai_prompt_versions version
		  JOIN outbound_ai_prompts prompt ON prompt.id = version.prompt_id
		 WHERE prompt.account_id = $1 AND prompt.name = $2 AND version.version = $3`, mustPgUUID(accountID), name, version))
}

// setOutboundAIPromptLive runs fn on the locked prompt; fn returns the
// version to make live, or ErrInvalidArgument.
func (s *PgStore) setOutboundAIPromptLive(ctx context.Context, accountID, name string, pick func(OutboundAIPrompt) (int, error)) (OutboundAIPrompt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIPrompt{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	prompt, err := lockOutboundAIPrompt(ctx, tx, accountID, name)
	if err != nil {
		return OutboundAIPrompt{}, err
	}
	version, err := pick(prompt)
	if err != nil {
		return OutboundAIPrompt{}, err
	}
	exists, err := outboundAIPromptHasVersion(ctx, tx, prompt.ID, version)
	if err != nil {
		return OutboundAIPrompt{}, err
	}
	if !exists {
		return OutboundAIPrompt{}, ErrInvalidArgument
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_prompt_canaries WHERE prompt_id = $1`, mustPgUUID(prompt.ID)); err != nil {
		return OutboundAIPrompt{}, mapErr(err)
	}
	prompt.Canary = nil
	prompt, err = updateOutboundAIPromptPointers(ctx, tx, prompt, promptVersionPtr(version), prompt.LatestVersion, nil)
	if err != nil {
		return OutboundAIPrompt{}, err
	}
	return prompt, mapErr(tx.Commit(ctx))
}

func (s *PgStore) SetOutboundAIPromptLive(ctx context.Context, accountID, name string, version int) (OutboundAIPrompt, error) {
	return s.setOutboundAIPromptLive(ctx, accountID, name, func(OutboundAIPrompt) (int, error) { return version, nil })
}

func (s *PgStore) RollBackOutboundAIPromptLive(ctx context.Context, accountID, name string) (OutboundAIPrompt, error) {
	return s.setOutboundAIPromptLive(ctx, accountID, name, func(prompt OutboundAIPrompt) (int, error) {
		if prompt.PreviousLiveVersion == nil {
			return 0, ErrInvalidArgument
		}
		return *prompt.PreviousLiveVersion, nil
	})
}

func (s *PgStore) PromoteOutboundAIPromptCanary(ctx context.Context, accountID, name string) (OutboundAIPrompt, error) {
	return s.setOutboundAIPromptLive(ctx, accountID, name, func(prompt OutboundAIPrompt) (int, error) {
		if prompt.Canary == nil || prompt.Canary.State != api.OutboundAIRouteCanaryActive {
			return 0, ErrNotFound
		}
		return prompt.Canary.Version, nil
	})
}

func (s *PgStore) DeleteOutboundAIPrompt(ctx context.Context, accountID, name string) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM outbound_ai_prompts WHERE account_id = $1 AND name = $2`, mustPgUUID(accountID), name)
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetOutboundAIPromptCanary starts or replaces a prompt's canary and clears
// the candidate's minute rows, so its window starts empty.
func (s *PgStore) SetOutboundAIPromptCanary(ctx context.Context, accountID, name string, canary OutboundAIPromptCanary) (OutboundAIPromptCanary, error) {
	if !validOutboundAIPromptCanary(canary) {
		return OutboundAIPromptCanary{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIPromptCanary{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	prompt, err := lockOutboundAIPrompt(ctx, tx, accountID, name)
	if err != nil {
		return OutboundAIPromptCanary{}, err
	}
	exists, err := outboundAIPromptHasVersion(ctx, tx, prompt.ID, canary.Version)
	if err != nil {
		return OutboundAIPromptCanary{}, err
	}
	if !exists || prompt.LiveVersion == nil || *prompt.LiveVersion == canary.Version {
		return OutboundAIPromptCanary{}, ErrInvalidArgument
	}
	if err := upsertOutboundAIPromptCanary(ctx, tx, prompt.ID, canary); err != nil {
		return OutboundAIPromptCanary{}, err
	}
	stored, err := lockOutboundAIPrompt(ctx, tx, accountID, name)
	if err != nil {
		return OutboundAIPromptCanary{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboundAIPromptCanary{}, mapErr(err)
	}
	return *stored.Canary, nil
}

func upsertOutboundAIPromptCanary(ctx context.Context, tx pgx.Tx, promptID string, canary OutboundAIPromptCanary) error {
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_prompt_version_minutes WHERE prompt_id = $1 AND version = $2`,
		mustPgUUID(promptID), canary.Version); err != nil {
		return mapErr(err)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO outbound_ai_prompt_canaries
		    (prompt_id, version, percent, max_error_rate_increase, max_latency_increase_percent, min_calls, window_minutes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (prompt_id) DO UPDATE SET
		    version = EXCLUDED.version, percent = EXCLUDED.percent, max_error_rate_increase = EXCLUDED.max_error_rate_increase,
		    max_latency_increase_percent = EXCLUDED.max_latency_increase_percent, min_calls = EXCLUDED.min_calls,
		    window_minutes = EXCLUDED.window_minutes, state = 'active', reason = NULL, started_at = now(), decided_at = NULL`,
		mustPgUUID(promptID), canary.Version, canary.Percent, canary.MaxErrorRateIncrease, canary.MaxLatencyIncreasePercent,
		canary.MinCalls, canary.WindowMinutes)
	return mapErr(err)
}

func (s *PgStore) DeleteOutboundAIPromptCanary(ctx context.Context, accountID, name string) error {
	command, err := s.pool.Exec(ctx, `
		DELETE FROM outbound_ai_prompt_canaries canary USING outbound_ai_prompts prompt
		 WHERE canary.prompt_id = prompt.id AND prompt.account_id = $1 AND prompt.name = $2`, mustPgUUID(accountID), name)
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// OutboundAIPromptStats sums each version's minute rows and the feedback
// on its logged calls over the stats window.
func (s *PgStore) OutboundAIPromptStats(ctx context.Context, accountID, name string) (OutboundAIPromptStatsWindow, error) {
	prompt, err := s.GetOutboundAIPrompt(ctx, accountID, name)
	if err != nil {
		return OutboundAIPromptStatsWindow{}, err
	}
	window := OutboundAIPromptStatsWindow{Prompt: prompt, Versions: []OutboundAIPromptVersionStats{}}
	rows, err := s.pool.Query(ctx, `
		WITH bounds AS (SELECT date_trunc('minute', now() - make_interval(mins => $2::int)) AS start),
		     minutes AS (
		       SELECT version, sum(calls)::bigint AS calls, sum(errors)::bigint AS errors, sum(ttfb_calls)::bigint AS ttfb_calls,
		              sum(ttfb_ms_total)::bigint AS ttfb_ms_total, sum(cost_millicents)::bigint AS cost_millicents
		         FROM outbound_ai_prompt_version_minutes, bounds
		        WHERE prompt_id = $1 AND minute >= bounds.start
		        GROUP BY version),
		     feedback AS (
		       SELECT prompt_version AS version, count(*) FILTER (WHERE feedback = 'good')::bigint AS good,
		              count(*) FILTER (WHERE feedback = 'bad')::bigint AS bad
		         FROM outbound_ai_request_log, bounds
		        WHERE prompt_id = $1 AND created_at >= bounds.start AND feedback IS NOT NULL
		        GROUP BY prompt_version)
		SELECT (SELECT start FROM bounds), COALESCE(minutes.version, feedback.version),
		       COALESCE(minutes.calls, 0), COALESCE(minutes.errors, 0), COALESCE(minutes.ttfb_calls, 0),
		       COALESCE(minutes.ttfb_ms_total, 0), COALESCE(minutes.cost_millicents, 0),
		       COALESCE(feedback.good, 0), COALESCE(feedback.bad, 0)
		  FROM minutes FULL JOIN feedback ON feedback.version = minutes.version
		 ORDER BY 2 DESC`, mustPgUUID(prompt.ID), api.OutboundAIPromptStatsWindowMinutes)
	if err != nil {
		return OutboundAIPromptStatsWindow{}, mapErr(err)
	}
	defer rows.Close()
	window.WindowStart = time.Now().UTC().Add(-api.OutboundAIPromptStatsWindowMinutes * time.Minute).Truncate(time.Minute)
	for rows.Next() {
		var stats OutboundAIPromptVersionStats
		var start time.Time
		if err := rows.Scan(&start, &stats.Version, &stats.Calls, &stats.Errors, &stats.TTFBCalls, &stats.TTFBMSTotal,
			&stats.CostMillicents, &stats.GoodFeedback, &stats.BadFeedback); err != nil {
			return OutboundAIPromptStatsWindow{}, mapErr(err)
		}
		window.WindowStart = start.UTC()
		window.Versions = append(window.Versions, stats)
	}
	return window, mapErr(rows.Err())
}

func (s *PgStore) CreateOutboundAIPromptEvaluation(ctx context.Context, evaluation OutboundAIPromptEvaluation) (OutboundAIPromptEvaluation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIPromptEvaluation{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	prompt, err := scanOutboundAIPrompt(tx.QueryRow(ctx, `SELECT `+outboundAIPromptColumns+` FROM `+outboundAIPromptFrom+`
		 WHERE prompt.account_id = $1 AND prompt.id = $2 FOR SHARE OF prompt`, mustPgUUID(evaluation.AccountID), mustPgUUID(evaluation.PromptID)))
	if err != nil {
		return OutboundAIPromptEvaluation{}, err
	}
	exists, err := outboundAIPromptHasVersion(ctx, tx, prompt.ID, evaluation.CandidateVersion)
	if err != nil {
		return OutboundAIPromptEvaluation{}, err
	}
	judge := api.OutboundAIPromptTarget{IntegrationID: evaluation.JudgeIntegrationID, Model: evaluation.JudgeModel}
	judgeOK, err := outboundAIPromptTargetAllowed(ctx, tx, evaluation.AccountID, judge)
	if err != nil {
		return OutboundAIPromptEvaluation{}, err
	}
	if !exists || !judgeOK || prompt.LiveVersion == nil || *prompt.LiveVersion == evaluation.CandidateVersion {
		return OutboundAIPromptEvaluation{}, ErrInvalidArgument
	}
	cases, err := json.Marshal(evaluation.Cases)
	if err != nil {
		return OutboundAIPromptEvaluation{}, err
	}
	var canary []byte
	if evaluation.Canary != nil {
		if canary, err = json.Marshal(evaluation.Canary); err != nil {
			return OutboundAIPromptEvaluation{}, err
		}
	}
	evaluation.PromptName, evaluation.BaselineVersion, evaluation.State = prompt.Name, *prompt.LiveVersion, api.OutboundAIEvaluationPending
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbound_ai_prompt_evaluations
		    (id, account_id, prompt_id, baseline_version, candidate_version, cases, case_count, judge_integration_id, judge_model,
		     max_loss_rate, canary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING created_at`, mustPgUUID(evaluation.ID), mustPgUUID(evaluation.AccountID), mustPgUUID(prompt.ID),
		evaluation.BaselineVersion, evaluation.CandidateVersion, cases, len(evaluation.Cases), mustPgUUID(evaluation.JudgeIntegrationID),
		evaluation.JudgeModel, evaluation.MaxLossRate, canary).Scan(&evaluation.CreatedAt); err != nil {
		return OutboundAIPromptEvaluation{}, mapErr(err)
	}
	evaluation.CreatedAt = evaluation.CreatedAt.UTC()
	return evaluation, mapErr(tx.Commit(ctx))
}

const outboundAIPromptEvaluationColumns = `evaluation.id, evaluation.account_id, evaluation.prompt_id, prompt.name,
       evaluation.baseline_version, evaluation.candidate_version, evaluation.cases, evaluation.judge_integration_id,
       evaluation.judge_model, evaluation.max_loss_rate, evaluation.canary, evaluation.state, COALESCE(evaluation.verdict, ''),
       COALESCE(evaluation.error_code, ''), evaluation.wins, evaluation.ties, evaluation.losses, evaluation.candidate_errors,
       evaluation.judge_errors, evaluation.baseline_cost_millicents, evaluation.candidate_cost_millicents,
       evaluation.baseline_ttfb_ms_total, evaluation.baseline_ttfb_calls, evaluation.candidate_ttfb_ms_total,
       evaluation.candidate_ttfb_calls, evaluation.canary_started, evaluation.created_at, evaluation.started_at,
       evaluation.finished_at`

func scanOutboundAIPromptEvaluation(row pgx.Row) (OutboundAIPromptEvaluation, error) {
	var e OutboundAIPromptEvaluation
	var id, accountID, promptID, judgeID pgtype.UUID
	var cases, canary []byte
	if err := row.Scan(&id, &accountID, &promptID, &e.PromptName, &e.BaselineVersion, &e.CandidateVersion, &cases, &judgeID,
		&e.JudgeModel, &e.MaxLossRate, &canary, &e.State, &e.Verdict, &e.ErrorCode, &e.Wins, &e.Ties, &e.Losses,
		&e.CandidateErrors, &e.JudgeErrors, &e.BaselineCostMillicents, &e.CandidateCostMillicents, &e.BaselineTTFBMSTotal,
		&e.BaselineTTFBCalls, &e.CandidateTTFBMSTotal, &e.CandidateTTFBCalls, &e.CanaryStarted, &e.CreatedAt, &e.StartedAt,
		&e.FinishedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OutboundAIPromptEvaluation{}, ErrNotFound
		}
		return OutboundAIPromptEvaluation{}, mapErr(err)
	}
	e.ID, e.AccountID, e.PromptID, e.JudgeIntegrationID = pgUUIDString(id), pgUUIDString(accountID), pgUUIDString(promptID), pgUUIDString(judgeID)
	e.CreatedAt = e.CreatedAt.UTC()
	if err := json.Unmarshal(cases, &e.Cases); err != nil {
		return OutboundAIPromptEvaluation{}, err
	}
	if len(canary) > 0 {
		e.Canary = &api.PutOutboundAIPromptCanaryRequest{}
		if err := json.Unmarshal(canary, e.Canary); err != nil {
			return OutboundAIPromptEvaluation{}, err
		}
	}
	return e, nil
}

func (s *PgStore) ListOutboundAIPromptEvaluations(ctx context.Context, accountID, name string) ([]OutboundAIPromptEvaluation, error) {
	prompt, err := s.GetOutboundAIPrompt(ctx, accountID, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+outboundAIPromptEvaluationColumns+`
		  FROM outbound_ai_prompt_evaluations evaluation JOIN outbound_ai_prompts prompt ON prompt.id = evaluation.prompt_id
		 WHERE evaluation.prompt_id = $1 ORDER BY evaluation.created_at DESC LIMIT 100`, mustPgUUID(prompt.ID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]OutboundAIPromptEvaluation, 0)
	for rows.Next() {
		evaluation, err := scanOutboundAIPromptEvaluation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, evaluation)
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) GetOutboundAIPromptEvaluation(ctx context.Context, accountID, evaluationID string) (OutboundAIPromptEvaluation, error) {
	evaluation, err := scanOutboundAIPromptEvaluation(s.pool.QueryRow(ctx, `SELECT `+outboundAIPromptEvaluationColumns+`
		  FROM outbound_ai_prompt_evaluations evaluation JOIN outbound_ai_prompts prompt ON prompt.id = evaluation.prompt_id
		 WHERE evaluation.account_id = $1 AND evaluation.id = $2`, mustPgUUID(accountID), mustPgUUID(evaluationID)))
	if err != nil {
		return OutboundAIPromptEvaluation{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT case_index, verdict, baseline_status, candidate_status, baseline_cost_millicents, candidate_cost_millicents,
		       baseline_ttfb_ms, candidate_ttfb_ms, baseline_output_text, candidate_output_text
		  FROM outbound_ai_prompt_evaluation_items WHERE evaluation_id = $1 ORDER BY case_index`, mustPgUUID(evaluationID))
	if err != nil {
		return OutboundAIPromptEvaluation{}, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item OutboundAIPromptEvaluationItem
		var baselineTTFB, candidateTTFB *int32
		if err := rows.Scan(&item.Case, &item.Verdict, &item.BaselineStatus, &item.CandidateStatus, &item.BaselineCostMillicents,
			&item.CandidateCostMillicents, &baselineTTFB, &candidateTTFB, &item.BaselineOutputText, &item.CandidateOutputText); err != nil {
			return OutboundAIPromptEvaluation{}, mapErr(err)
		}
		item.BaselineTTFBMS, item.CandidateTTFBMS = widenTTFB(baselineTTFB), widenTTFB(candidateTTFB)
		evaluation.Items = append(evaluation.Items, item)
	}
	return evaluation, mapErr(rows.Err())
}

func widenTTFB(value *int32) *int64 {
	if value == nil {
		return nil
	}
	wide := int64(*value)
	return &wide
}
