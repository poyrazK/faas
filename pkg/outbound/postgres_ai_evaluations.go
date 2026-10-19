package outbound

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ AIEvaluationStore = (*PostgresBackend)(nil)

// ClaimAIEvaluation fails evaluations that stopped mid-run, then claims the
// oldest pending one with the route's two targets and the first target's
// latest replayable captures (ADR-1431). An evaluation whose target is gone
// fails without a claim.
func (b *PostgresBackend) ClaimAIEvaluation(ctx context.Context) (AIEvaluationJob, bool, error) {
	if _, err := b.pool.Exec(ctx, `
		UPDATE outbound_ai_evaluations SET state = 'failed', error_code = 'outbound_ai_evaluation_interrupted', finished_at = now()
		 WHERE state = 'running' AND started_at < now() - make_interval(mins => $1::int)`,
		api.OutboundAIEvaluationInterruptedAfterMinutes); err != nil {
		return AIEvaluationJob{}, false, err
	}
	var id, accountID, routeID, judgeID uuid.UUID
	var sampleSize int
	job := AIEvaluationJob{}
	err := b.pool.QueryRow(ctx, `
		UPDATE outbound_ai_evaluations evaluation SET state = 'running', started_at = now()
		 WHERE evaluation.id = (SELECT pending.id FROM outbound_ai_evaluations pending
		                         WHERE pending.state = 'pending' ORDER BY pending.created_at
		                         LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING evaluation.id, evaluation.account_id, evaluation.route_id, evaluation.position,
		          evaluation.judge_integration_id, evaluation.judge_model, evaluation.sample_size, evaluation.max_loss_rate`).
		Scan(&id, &accountID, &routeID, &job.Position, &judgeID, &job.JudgeModel, &sampleSize, &job.MaxLossRate)
	if errors.Is(err, pgx.ErrNoRows) {
		return AIEvaluationJob{}, false, nil
	}
	if err != nil {
		return AIEvaluationJob{}, false, err
	}
	job.ID, job.AccountID, job.RouteID, job.JudgeIntegrationID = id.String(), accountID.String(), routeID.String(), judgeID.String()
	rows, err := b.pool.Query(ctx, `
		SELECT position, integration_id::text, COALESCE(model, '') FROM outbound_ai_route_targets
		 WHERE route_id = $1 AND position IN (1, $2)`, routeID, job.Position)
	if err != nil {
		return AIEvaluationJob{}, false, err
	}
	for rows.Next() {
		var position int
		var target AIRouteTarget
		if err := rows.Scan(&position, &target.IntegrationID, &target.Model); err != nil {
			rows.Close()
			return AIEvaluationJob{}, false, err
		}
		if position == 1 {
			job.Baseline = target
		} else {
			job.Candidate = target
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return AIEvaluationJob{}, false, err
	}
	if job.Baseline.IntegrationID == "" || job.Candidate.IntegrationID == "" {
		_, err := b.FinishAIEvaluation(ctx, job, nil, "outbound_ai_evaluation_target_unavailable")
		return AIEvaluationJob{}, false, err
	}
	if job.Samples, err = b.aiEvaluationSamples(ctx, job.Baseline.IntegrationID, sampleSize); err != nil {
		return AIEvaluationJob{}, false, err
	}
	return job, true, nil
}

// aiEvaluationSamples reads an integration's latest captured calls that
// can replay on any provider: 2xx, untruncated, OpenAI chat completions or
// Responses, sent by an app rather than by the gateway itself.
func (b *PostgresBackend) aiEvaluationSamples(ctx context.Context, integrationID string, limit int) ([]AIEvaluationSample, error) {
	id, err := uuid.Parse(integrationID)
	if err != nil {
		return nil, err
	}
	rows, err := b.pool.Query(ctx, `
		SELECT log.id::text, log.model, content.request_path, content.request_body, content.response_body,
		       COALESCE(content.response_content_type, ''), log.cost_millicents, log.ttfb_ms
		  FROM outbound_ai_request_log log
		  JOIN outbound_ai_request_content content ON content.log_id = log.id
		 WHERE log.integration_id = $1 AND log.status BETWEEN 200 AND 299
		   AND log.caller NOT IN ('replay', 'moderation', 'evaluation')
		   AND NOT content.request_truncated AND NOT content.response_truncated
		   AND (content.request_path LIKE '%/chat/completions' OR content.request_path LIKE '%/responses')
		 ORDER BY log.created_at DESC, log.id DESC
		 LIMIT $2`, id, min(max(limit, 1), api.OutboundAIEvaluationSampleMax))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []AIEvaluationSample
	for rows.Next() {
		var sample AIEvaluationSample
		var ttfb *int32
		if err := rows.Scan(&sample.LogID, &sample.SourceModel, &sample.RequestPath, &sample.RequestBody, &sample.ResponseBody,
			&sample.ResponseContentType, &sample.CostMillicents, &ttfb); err != nil {
			return nil, err
		}
		if ttfb != nil {
			value := int64(*ttfb)
			sample.TTFBMS = &value
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

// FinishAIEvaluation stores an evaluation's items and totals and ends it.
// A passing evaluation starts its canary when the evaluated target is still
// at its position and the route has no active canary. An evaluation that
// is no longer running, because it was interrupted or its route deleted,
// is left as it is.
func (b *PostgresBackend) FinishAIEvaluation(ctx context.Context, job AIEvaluationJob, items []AIEvaluationItem, errorCode string) (AIEvaluationVerdict, error) {
	evaluationID, err := uuid.Parse(job.ID)
	if err != nil {
		return AIEvaluationVerdict{}, err
	}
	routeID, err := uuid.Parse(job.RouteID)
	if err != nil {
		return AIEvaluationVerdict{}, err
	}
	verdict := JudgeAIEvaluation(items, job.MaxLossRate)
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
	totals := aiEvaluationTotals(items)
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return AIEvaluationVerdict{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The route is locked first, as apid locks it before its canaries and
	// evaluations.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM outbound_ai_routes WHERE id = $1 FOR UPDATE`, routeID); err != nil {
		return AIEvaluationVerdict{}, err
	}
	var canary []byte
	err = tx.QueryRow(ctx, `
		UPDATE outbound_ai_evaluations
		   SET state = $2, verdict = NULLIF($3, ''), error_code = NULLIF($4, ''), samples = $5, wins = $6, ties = $7, losses = $8,
		       candidate_errors = $9, judge_errors = $10, baseline_cost_millicents = $11, candidate_cost_millicents = $12,
		       baseline_ttfb_ms_total = $13, baseline_ttfb_calls = $14, candidate_ttfb_ms_total = $15,
		       candidate_ttfb_calls = $16, finished_at = now()
		 WHERE id = $1 AND state = 'running'
		RETURNING canary`,
		evaluationID, state, verdictText, errorCode, len(items), verdict.Wins, verdict.Ties, verdict.Losses,
		verdict.CandidateErrors, verdict.JudgeErrors, totals.baselineCost, totals.candidateCost,
		totals.baselineTTFB, totals.baselineTTFBCalls, totals.candidateTTFB, totals.candidateTTFBCalls).Scan(&canary)
	if errors.Is(err, pgx.ErrNoRows) {
		return verdict, nil
	}
	if err != nil {
		return AIEvaluationVerdict{}, err
	}
	for ordinal, item := range items {
		if err := insertAIEvaluationItem(ctx, tx, evaluationID, ordinal, item); err != nil {
			return AIEvaluationVerdict{}, err
		}
	}
	if errorCode == "" && verdict.Pass && len(canary) > 0 {
		if verdict.CanaryStarted, err = startEvaluatedCanary(ctx, tx, job, routeID, canary); err != nil {
			return AIEvaluationVerdict{}, err
		}
		if verdict.CanaryStarted {
			if _, err := tx.Exec(ctx, `UPDATE outbound_ai_evaluations SET canary_started = true WHERE id = $1`, evaluationID); err != nil {
				return AIEvaluationVerdict{}, err
			}
		}
	}
	return verdict, tx.Commit(ctx)
}

type aiEvaluationTotalsRow struct {
	baselineCost, candidateCost           int64
	baselineTTFB, candidateTTFB           int64
	baselineTTFBCalls, candidateTTFBCalls int
}

func aiEvaluationTotals(items []AIEvaluationItem) aiEvaluationTotalsRow {
	var totals aiEvaluationTotalsRow
	for _, item := range items {
		totals.baselineCost += max(item.BaselineCostMillicents, 0)
		totals.candidateCost += max(item.CandidateCostMillicents, 0)
		if item.BaselineTTFBMS != nil {
			totals.baselineTTFB += max(*item.BaselineTTFBMS, 0)
			totals.baselineTTFBCalls++
		}
		if item.CandidateTTFBMS != nil && item.Verdict != api.OutboundAIEvaluationItemCandidateError {
			totals.candidateTTFB += max(*item.CandidateTTFBMS, 0)
			totals.candidateTTFBCalls++
		}
	}
	return totals
}

func insertAIEvaluationItem(ctx context.Context, tx pgx.Tx, evaluationID uuid.UUID, ordinal int, item AIEvaluationItem) error {
	logID, err := uuid.Parse(item.LogID)
	if err != nil {
		return err
	}
	switch item.Verdict {
	case api.OutboundAIEvaluationItemWin, api.OutboundAIEvaluationItemTie, api.OutboundAIEvaluationItemLoss,
		api.OutboundAIEvaluationItemCandidateError, api.OutboundAIEvaluationItemJudgeError:
	default:
		item.Verdict = api.OutboundAIEvaluationItemJudgeError
	}
	var status *int
	if item.CandidateStatus >= 100 && item.CandidateStatus <= 599 {
		status = &item.CandidateStatus
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbound_ai_evaluation_items
		    (evaluation_id, ordinal, log_id, verdict, candidate_status, candidate_cost_millicents, candidate_ttfb_ms,
		     baseline_cost_millicents, baseline_ttfb_ms, baseline_output_text, candidate_output_text)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		evaluationID, ordinal, logID, item.Verdict, status, max(item.CandidateCostMillicents, 0), clampedMS(item.CandidateTTFBMS),
		max(item.BaselineCostMillicents, 0), clampedMS(item.BaselineTTFBMS),
		truncateUTF8(item.BaselineOutputText, api.OutboundAIReplayOutputTextMaxBytes),
		truncateUTF8(item.CandidateOutputText, api.OutboundAIReplayOutputTextMaxBytes))
	return err
}

func clampedMS(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clamped := min(max(*value, 0), 1<<31-1)
	return &clamped
}

// startEvaluatedCanary starts the canary an evaluation was created with,
// as apid would (ADR-1340), when the evaluated target is unchanged and the
// route has no active canary. The route is already locked.
func startEvaluatedCanary(ctx context.Context, tx pgx.Tx, job AIEvaluationJob, routeID uuid.UUID, raw []byte) (bool, error) {
	var request api.PutOutboundAIRouteCanaryRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return false, nil //nolint:nilerr // a stored canary that does not parse starts nothing; the verdict still stands.
	}
	request = request.Normalized()
	if !request.Valid() || request.Target != job.Position {
		return false, nil
	}
	var integrationID, model string
	var active bool
	err := tx.QueryRow(ctx, `
		SELECT target.integration_id::text, COALESCE(target.model, ''),
		       EXISTS (SELECT 1 FROM outbound_ai_route_canaries canary WHERE canary.route_id = target.route_id AND canary.state = 'active')
		  FROM outbound_ai_route_targets target
		 WHERE target.route_id = $1 AND target.position = $2`, routeID, job.Position).Scan(&integrationID, &model, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if active || integrationID != job.Candidate.IntegrationID || model != job.Candidate.Model {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_route_canaries WHERE route_id = $1`, routeID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_route_target_minutes WHERE route_id = $1`, routeID); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbound_ai_route_canaries
		    (route_id, position, percent, max_error_rate_increase, max_latency_increase_percent, min_calls, window_minutes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, routeID, request.Target, request.Percent, *request.MaxErrorRateIncrease,
		request.MaxLatencyIncreasePercent, *request.MinCalls, *request.WindowMinutes)
	return err == nil, err
}
