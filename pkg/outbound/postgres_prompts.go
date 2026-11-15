package outbound

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
)

// PostgresPromptResolver reads managed prompts that apid writes
// (ADR 20261114180740).
type PostgresPromptResolver struct{ pool *pgxpool.Pool }

func NewPostgresPromptResolver(pool *pgxpool.Pool) (*PostgresPromptResolver, error) {
	if pool == nil {
		return nil, errors.New("outbound prompt resolver: postgres pool is required")
	}
	return &PostgresPromptResolver{pool: pool}, nil
}

// Prompt reads the prompt with its live version, its active canary's
// version and the pinned version in one query.
func (r *PostgresPromptResolver) Prompt(ctx context.Context, accountID, name string, pinned int) (AIPrompt, error) {
	account, err := uuid.Parse(accountID)
	if err != nil || !api.ValidOutboundAIPromptName(name) {
		return AIPrompt{}, ErrPromptNotFound
	}
	rows, err := r.pool.Query(ctx, `
		SELECT prompt.id::text, COALESCE(prompt.live_version, 0), COALESCE(canary.version, 0), COALESCE(canary.percent, 0),
		       version.version, version.messages, version.defaults, version.params,
		       COALESCE(version.route_id::text, ''), COALESCE(version.integration_id::text, ''), COALESCE(version.model, '')
		  FROM outbound_ai_prompts prompt
		  LEFT JOIN outbound_ai_prompt_canaries canary ON canary.prompt_id = prompt.id AND canary.state = 'active'
		  LEFT JOIN outbound_ai_prompt_versions version ON version.prompt_id = prompt.id
		       AND version.version IN (prompt.live_version, canary.version, $3)
		 WHERE prompt.account_id = $1 AND prompt.name = $2`, account, name, pinned)
	if err != nil {
		return AIPrompt{}, err
	}
	defer rows.Close()
	prompt := AIPrompt{AccountID: accountID, Versions: map[int]AIPromptVersion{}}
	found := false
	for rows.Next() {
		found = true
		var number *int
		var messages, defaults, params []byte
		var version AIPromptVersion
		if err := rows.Scan(&prompt.ID, &prompt.LiveVersion, &prompt.CanaryVersion, &prompt.CanaryPercent, &number, &messages,
			&defaults, &params, &version.Target.RouteID, &version.Target.IntegrationID, &version.Target.Model); err != nil {
			return AIPrompt{}, err
		}
		if number == nil {
			continue
		}
		version.Version = *number
		if err := decodePromptVersion(&version, messages, defaults, params); err != nil {
			return AIPrompt{}, err
		}
		prompt.Versions[version.Version] = version
	}
	if err := rows.Err(); err != nil {
		return AIPrompt{}, err
	}
	if !found {
		return AIPrompt{}, ErrPromptNotFound
	}
	return prompt, nil
}

func decodePromptVersion(version *AIPromptVersion, messages, defaults, params []byte) error {
	if err := json.Unmarshal(messages, &version.Messages); err != nil {
		return err
	}
	if err := json.Unmarshal(defaults, &version.Defaults); err != nil {
		return err
	}
	return json.Unmarshal(params, &version.Params)
}

var _ PromptResolver = (*PostgresPromptResolver)(nil)

// ActiveAIPromptCanaries reads every active prompt canary with the live
// version's and the candidate's counts over the canary's window: from the
// later of its start and the window's start, by whole minutes.
func (b *PostgresBackend) ActiveAIPromptCanaries(ctx context.Context) ([]AIPromptCanaryJudgement, error) {
	rows, err := b.pool.Query(ctx, `
		SELECT canary.prompt_id::text, canary.version, canary.max_error_rate_increase, canary.max_latency_increase_percent,
		       canary.min_calls,
		       COALESCE(sum(stats.calls) FILTER (WHERE stats.version = prompt.live_version), 0)::bigint,
		       COALESCE(sum(stats.errors) FILTER (WHERE stats.version = prompt.live_version), 0)::bigint,
		       COALESCE(sum(stats.ttfb_calls) FILTER (WHERE stats.version = prompt.live_version), 0)::bigint,
		       COALESCE(sum(stats.ttfb_ms_total) FILTER (WHERE stats.version = prompt.live_version), 0)::bigint,
		       COALESCE(sum(stats.calls) FILTER (WHERE stats.version = canary.version), 0)::bigint,
		       COALESCE(sum(stats.errors) FILTER (WHERE stats.version = canary.version), 0)::bigint,
		       COALESCE(sum(stats.ttfb_calls) FILTER (WHERE stats.version = canary.version), 0)::bigint,
		       COALESCE(sum(stats.ttfb_ms_total) FILTER (WHERE stats.version = canary.version), 0)::bigint
		  FROM outbound_ai_prompt_canaries canary
		  JOIN outbound_ai_prompts prompt ON prompt.id = canary.prompt_id
		  LEFT JOIN outbound_ai_prompt_version_minutes stats
		    ON stats.prompt_id = canary.prompt_id AND stats.version IN (prompt.live_version, canary.version)
		   AND stats.minute >= date_trunc('minute', greatest(canary.started_at,
		                                                      now() - make_interval(mins => canary.window_minutes::int)))
		 WHERE canary.state = 'active'
		 GROUP BY canary.prompt_id, canary.version, canary.max_error_rate_increase, canary.max_latency_increase_percent,
		          canary.min_calls`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var judgements []AIPromptCanaryJudgement
	for rows.Next() {
		var j AIPromptCanaryJudgement
		if err := rows.Scan(&j.PromptID, &j.Version, &j.Rule.MaxErrorRateIncrease, &j.Rule.MaxLatencyIncreasePercent, &j.Rule.MinCalls,
			&j.Rule.Baseline.Calls, &j.Rule.Baseline.Errors, &j.Rule.Baseline.TTFBCalls, &j.Rule.Baseline.TTFBMSTotal,
			&j.Rule.Canary.Calls, &j.Rule.Canary.Errors, &j.Rule.Canary.TTFBCalls, &j.Rule.Canary.TTFBMSTotal); err != nil {
			return nil, err
		}
		judgements = append(judgements, j)
	}
	return judgements, rows.Err()
}

// RollBackAIPromptCanary rolls back a prompt canary still active on the
// judged version, so a canary apid replaced meanwhile is left alone.
func (b *PostgresBackend) RollBackAIPromptCanary(ctx context.Context, promptID string, version int, reason string) (bool, error) {
	id, err := uuid.Parse(promptID)
	if err != nil {
		return false, err
	}
	command, err := b.pool.Exec(ctx, `
		UPDATE outbound_ai_prompt_canaries SET state = 'rolled_back', reason = $3, decided_at = now()
		 WHERE prompt_id = $1 AND version = $2 AND state = 'active'`, id, version, reason)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

var _ AIPromptCanaryStore = (*PostgresBackend)(nil)

// ClaimAIPromptEvaluation fails prompt evaluations that stopped mid-run,
// then claims a pending one with both versions, fairly across accounts as
// route evaluations are claimed. An evaluation whose version is gone fails
// without a claim.
func (b *PostgresBackend) ClaimAIPromptEvaluation(ctx context.Context) (AIPromptEvaluationJob, bool, error) {
	if _, err := b.pool.Exec(ctx, `
		UPDATE outbound_ai_prompt_evaluations SET state = 'failed', error_code = 'outbound_ai_evaluation_interrupted', finished_at = now()
		 WHERE state = 'running' AND started_at < now() - make_interval(mins => $1::int)`,
		api.OutboundAIEvaluationInterruptedAfterMinutes); err != nil {
		return AIPromptEvaluationJob{}, false, err
	}
	var id, accountID, promptID, judgeID uuid.UUID
	var cases, canary []byte
	var baseline, candidate int
	job := AIPromptEvaluationJob{}
	err := b.pool.QueryRow(ctx, `
		UPDATE outbound_ai_prompt_evaluations evaluation SET state = 'running', started_at = now()
		 WHERE evaluation.id = (SELECT pending.id FROM outbound_ai_prompt_evaluations pending
		                         WHERE pending.state = 'pending'
		                         ORDER BY (SELECT count(*) FROM outbound_ai_prompt_evaluations running
		                                    WHERE running.account_id = pending.account_id AND running.state = 'running'),
		                                  pending.created_at
		                         LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING evaluation.id, evaluation.account_id, evaluation.prompt_id, evaluation.baseline_version,
		          evaluation.candidate_version, evaluation.cases, evaluation.judge_integration_id, evaluation.judge_model,
		          evaluation.max_loss_rate, evaluation.canary`).
		Scan(&id, &accountID, &promptID, &baseline, &candidate, &cases, &judgeID, &job.JudgeModel, &job.MaxLossRate, &canary)
	if errors.Is(err, pgx.ErrNoRows) {
		return AIPromptEvaluationJob{}, false, nil
	}
	if err != nil {
		return AIPromptEvaluationJob{}, false, err
	}
	job.ID, job.AccountID, job.PromptID, job.JudgeIntegrationID = id.String(), accountID.String(), promptID.String(), judgeID.String()
	job.Canary = canary
	if err := json.Unmarshal(cases, &job.Cases); err != nil {
		_, err := b.FinishAIPromptEvaluation(ctx, job, nil, "outbound_ai_evaluation_failed")
		return AIPromptEvaluationJob{}, false, err
	}
	versions, err := b.promptVersions(ctx, promptID, baseline, candidate)
	if err != nil {
		return AIPromptEvaluationJob{}, false, err
	}
	job.Baseline, job.Candidate = versions[baseline], versions[candidate]
	if job.Baseline.Version == 0 || job.Candidate.Version == 0 {
		_, err := b.FinishAIPromptEvaluation(ctx, job, nil, "outbound_ai_evaluation_target_unavailable")
		return AIPromptEvaluationJob{}, false, err
	}
	return job, true, nil
}

func (b *PostgresBackend) promptVersions(ctx context.Context, promptID uuid.UUID, numbers ...int) (map[int]AIPromptVersion, error) {
	rows, err := b.pool.Query(ctx, `
		SELECT version, messages, defaults, params, COALESCE(route_id::text, ''), COALESCE(integration_id::text, ''),
		       COALESCE(model, '')
		  FROM outbound_ai_prompt_versions WHERE prompt_id = $1 AND version = ANY($2)`, promptID, numbers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]AIPromptVersion{}
	for rows.Next() {
		var version AIPromptVersion
		var messages, defaults, params []byte
		if err := rows.Scan(&version.Version, &messages, &defaults, &params, &version.Target.RouteID, &version.Target.IntegrationID,
			&version.Target.Model); err != nil {
			return nil, err
		}
		if err := decodePromptVersion(&version, messages, defaults, params); err != nil {
			return nil, err
		}
		out[version.Version] = version
	}
	return out, rows.Err()
}

// FinishAIPromptEvaluation stores a prompt evaluation's items and totals
// and ends it. A passing evaluation starts its canary when the baseline is
// still live and the prompt has no active canary. An evaluation that is no
// longer running is left as it is.
func (b *PostgresBackend) FinishAIPromptEvaluation(ctx context.Context, job AIPromptEvaluationJob, items []AIPromptEvaluationItem,
	errorCode string) (AIEvaluationVerdict, error) {
	evaluationID, err := uuid.Parse(job.ID)
	if err != nil {
		return AIEvaluationVerdict{}, err
	}
	verdict := JudgeAIEvaluation(promptItemsAsRouteItems(items), job.MaxLossRate)
	if errorCode != "" && !replayErrorCode.MatchString(errorCode) {
		errorCode = "outbound_ai_evaluation_failed"
	}
	state, verdictText := api.OutboundAIEvaluationFailed, ""
	if errorCode == "" {
		state, verdictText = api.OutboundAIEvaluationDone, api.OutboundAIEvaluationFail
		if verdict.Pass {
			verdictText = api.OutboundAIEvaluationPass
		}
	}
	totals := aiEvaluationTotals(promptItemsAsRouteItems(items))
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return AIEvaluationVerdict{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The prompt is locked first, as apid locks it before its canary.
	var liveVersion *int
	err = tx.QueryRow(ctx, `SELECT live_version FROM outbound_ai_prompts WHERE id = $1 FOR UPDATE`, uuid.MustParse(job.PromptID)).Scan(&liveVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return verdict, nil
	}
	if err != nil {
		return AIEvaluationVerdict{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE outbound_ai_prompt_evaluations
		   SET state = $2, verdict = NULLIF($3, ''), error_code = NULLIF($4, ''), wins = $5, ties = $6, losses = $7,
		       candidate_errors = $8, judge_errors = $9, baseline_cost_millicents = $10, candidate_cost_millicents = $11,
		       baseline_ttfb_ms_total = $12, baseline_ttfb_calls = $13, candidate_ttfb_ms_total = $14,
		       candidate_ttfb_calls = $15, finished_at = now()
		 WHERE id = $1 AND state = 'running'`,
		evaluationID, state, verdictText, errorCode, verdict.Wins, verdict.Ties, verdict.Losses, verdict.CandidateErrors,
		verdict.JudgeErrors, totals.baselineCost, totals.candidateCost, totals.baselineTTFB, totals.baselineTTFBCalls,
		totals.candidateTTFB, totals.candidateTTFBCalls)
	if err != nil {
		return AIEvaluationVerdict{}, err
	}
	if command.RowsAffected() == 0 {
		return verdict, nil
	}
	for _, item := range items {
		if err := insertAIPromptEvaluationItem(ctx, tx, evaluationID, item); err != nil {
			return AIEvaluationVerdict{}, err
		}
	}
	if errorCode == "" && verdict.Pass && len(job.Canary) > 0 && liveVersion != nil && *liveVersion == job.Baseline.Version {
		if verdict.CanaryStarted, err = startEvaluatedPromptCanary(ctx, tx, job); err != nil {
			return AIEvaluationVerdict{}, err
		}
		if verdict.CanaryStarted {
			if _, err := tx.Exec(ctx, `UPDATE outbound_ai_prompt_evaluations SET canary_started = true WHERE id = $1`, evaluationID); err != nil {
				return AIEvaluationVerdict{}, err
			}
		}
	}
	return verdict, tx.Commit(ctx)
}

func insertAIPromptEvaluationItem(ctx context.Context, tx pgx.Tx, evaluationID uuid.UUID, item AIPromptEvaluationItem) error {
	switch item.Verdict {
	case api.OutboundAIEvaluationItemWin, api.OutboundAIEvaluationItemTie, api.OutboundAIEvaluationItemLoss,
		api.OutboundAIEvaluationItemCandidateError, api.OutboundAIEvaluationItemJudgeError:
	default:
		item.Verdict = api.OutboundAIEvaluationItemJudgeError
	}
	status := func(value int) *int {
		if value >= 100 && value <= 599 {
			return &value
		}
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO outbound_ai_prompt_evaluation_items
		    (evaluation_id, case_index, verdict, baseline_status, candidate_status, baseline_cost_millicents,
		     candidate_cost_millicents, baseline_ttfb_ms, candidate_ttfb_ms, baseline_output_text, candidate_output_text)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (evaluation_id, case_index) DO NOTHING`,
		evaluationID, item.Case, item.Verdict, status(item.BaselineStatus), status(item.CandidateStatus),
		max(item.BaselineCostMillicents, 0), max(item.CandidateCostMillicents, 0), clampedMS(item.BaselineTTFBMS),
		clampedMS(item.CandidateTTFBMS), truncateUTF8(item.BaselineOutputText, api.OutboundAIReplayOutputTextMaxBytes),
		truncateUTF8(item.CandidateOutputText, api.OutboundAIReplayOutputTextMaxBytes))
	return err
}

// startEvaluatedPromptCanary starts the canary a prompt evaluation was
// created with, as apid would, when the prompt has no active canary. The
// prompt is already locked and its live version checked.
func startEvaluatedPromptCanary(ctx context.Context, tx pgx.Tx, job AIPromptEvaluationJob) (bool, error) {
	var request api.PutOutboundAIPromptCanaryRequest
	if err := json.Unmarshal(job.Canary, &request); err != nil {
		return false, nil //nolint:nilerr // a stored canary that does not parse starts nothing; the verdict still stands.
	}
	request = request.Normalized()
	if !request.Valid() || request.Version != job.Candidate.Version {
		return false, nil
	}
	promptID := uuid.MustParse(job.PromptID)
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outbound_ai_prompt_canaries WHERE prompt_id = $1 AND state = 'active')`,
		promptID).Scan(&active); err != nil || active {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_prompt_version_minutes WHERE prompt_id = $1 AND version = $2`,
		promptID, request.Version); err != nil {
		return false, err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO outbound_ai_prompt_canaries
		    (prompt_id, version, percent, max_error_rate_increase, max_latency_increase_percent, min_calls, window_minutes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (prompt_id) DO UPDATE SET
		    version = EXCLUDED.version, percent = EXCLUDED.percent, max_error_rate_increase = EXCLUDED.max_error_rate_increase,
		    max_latency_increase_percent = EXCLUDED.max_latency_increase_percent, min_calls = EXCLUDED.min_calls,
		    window_minutes = EXCLUDED.window_minutes, state = 'active', reason = NULL, started_at = now(), decided_at = NULL`,
		promptID, request.Version, request.Percent, *request.MaxErrorRateIncrease, request.MaxLatencyIncreasePercent,
		*request.MinCalls, *request.WindowMinutes)
	return err == nil, err
}

var _ AIPromptEvaluationStore = (*PostgresBackend)(nil)
