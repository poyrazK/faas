package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIEvaluationStore = (*PgStore)(nil)

const outboundAIEvaluationColumns = `id::text, account_id::text, route_id::text, position, judge_integration_id::text, judge_model,
       sample_size, max_loss_rate, canary, state, COALESCE(verdict, ''), COALESCE(error_code, ''), samples, wins, ties,
       losses, candidate_errors, judge_errors, baseline_cost_millicents, candidate_cost_millicents,
       baseline_ttfb_ms_total, baseline_ttfb_calls, candidate_ttfb_ms_total, candidate_ttfb_calls, canary_started,
       created_at, started_at, finished_at`

func scanOutboundAIEvaluation(row pgx.Row) (OutboundAIEvaluation, error) {
	var e OutboundAIEvaluation
	var canary []byte
	if err := row.Scan(&e.ID, &e.AccountID, &e.RouteID, &e.Position, &e.JudgeIntegrationID, &e.JudgeModel, &e.SampleSize,
		&e.MaxLossRate, &canary, &e.State, &e.Verdict, &e.ErrorCode, &e.Samples, &e.Wins, &e.Ties, &e.Losses,
		&e.CandidateErrors, &e.JudgeErrors, &e.BaselineCostMillicents, &e.CandidateCostMillicents, &e.BaselineTTFBMSTotal,
		&e.BaselineTTFBCalls, &e.CandidateTTFBMSTotal, &e.CandidateTTFBCalls, &e.CanaryStarted, &e.CreatedAt,
		&e.StartedAt, &e.FinishedAt); err != nil {
		return OutboundAIEvaluation{}, err
	}
	if len(canary) > 0 {
		var request api.PutOutboundAIRouteCanaryRequest
		if err := json.Unmarshal(canary, &request); err != nil {
			return OutboundAIEvaluation{}, err
		}
		e.Canary = &request
	}
	e.CreatedAt = e.CreatedAt.UTC()
	return e, nil
}

// CreateOutboundAIEvaluation records a pending evaluation (ADR-1431).
func (s *PgStore) CreateOutboundAIEvaluation(ctx context.Context, evaluation OutboundAIEvaluation) (OutboundAIEvaluation, error) {
	var canary []byte
	if evaluation.Canary != nil {
		var err error
		if canary, err = json.Marshal(evaluation.Canary); err != nil {
			return OutboundAIEvaluation{}, ErrInvalidArgument
		}
	}
	if evaluation.ID == "" {
		evaluation.ID = uuid.NewString()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIEvaluation{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	targets, err := lockOutboundAIRoute(ctx, tx, evaluation.AccountID, evaluation.RouteID)
	if err != nil {
		return OutboundAIEvaluation{}, err
	}
	if evaluation.Position < 2 || evaluation.Position > targets {
		return OutboundAIEvaluation{}, ErrInvalidArgument
	}
	var judge bool
	var today int
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM outbound_integrations integration
		                WHERE integration.account_id = $1 AND integration.id = $3 AND integration.enabled
		                  AND integration.ai_provider IS NOT NULL AND integration.owner_kind = 'customer'),
		       (SELECT count(*) FROM outbound_ai_evaluations
		         WHERE route_id = $2 AND created_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')`,
		mustPgUUID(evaluation.AccountID), mustPgUUID(evaluation.RouteID), mustPgUUID(evaluation.JudgeIntegrationID)).
		Scan(&judge, &today); err != nil {
		return OutboundAIEvaluation{}, mapErr(err)
	}
	if !judge {
		return OutboundAIEvaluation{}, ErrInvalidArgument
	}
	if today >= api.OutboundAIEvaluationsPerDay {
		return OutboundAIEvaluation{}, ErrOutboundAIEvaluationLimit
	}
	created, err := scanOutboundAIEvaluation(tx.QueryRow(ctx, `
		INSERT INTO outbound_ai_evaluations
		    (id, account_id, route_id, position, judge_integration_id, judge_model, sample_size, max_loss_rate, canary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+outboundAIEvaluationColumns, mustPgUUID(evaluation.ID), mustPgUUID(evaluation.AccountID),
		mustPgUUID(evaluation.RouteID), evaluation.Position, mustPgUUID(evaluation.JudgeIntegrationID), evaluation.JudgeModel,
		evaluation.SampleSize, evaluation.MaxLossRate, canary))
	if err != nil {
		return OutboundAIEvaluation{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboundAIEvaluation{}, mapErr(err)
	}
	return created, nil
}

// ListOutboundAIEvaluations lists a route's latest evaluations.
func (s *PgStore) ListOutboundAIEvaluations(ctx context.Context, accountID, routeID string) ([]OutboundAIEvaluation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+outboundAIEvaluationColumns+`
		  FROM outbound_ai_evaluations WHERE account_id = $1 AND route_id = $2
		 ORDER BY created_at DESC, id LIMIT $3`, mustPgUUID(accountID), mustPgUUID(routeID), outboundAIEvaluationsListMax)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]OutboundAIEvaluation, 0)
	for rows.Next() {
		evaluation, err := scanOutboundAIEvaluation(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, evaluation)
	}
	return out, mapErr(rows.Err())
}

// GetOutboundAIEvaluation reads an evaluation with its items.
func (s *PgStore) GetOutboundAIEvaluation(ctx context.Context, accountID, evaluationID string) (OutboundAIEvaluation, error) {
	evaluation, err := scanOutboundAIEvaluation(s.pool.QueryRow(ctx, `
		SELECT `+outboundAIEvaluationColumns+`
		  FROM outbound_ai_evaluations WHERE account_id = $1 AND id = $2`, mustPgUUID(accountID), mustPgUUID(evaluationID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIEvaluation{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIEvaluation{}, mapErr(err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT log_id::text, verdict, candidate_status, candidate_cost_millicents, candidate_ttfb_ms,
		       baseline_cost_millicents, baseline_ttfb_ms, baseline_output_text, candidate_output_text
		  FROM outbound_ai_evaluation_items WHERE evaluation_id = $1 ORDER BY ordinal`, mustPgUUID(evaluationID))
	if err != nil {
		return OutboundAIEvaluation{}, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item OutboundAIEvaluationItem
		var status *int16
		var candidateTTFB, baselineTTFB *int32
		if err := rows.Scan(&item.LogID, &item.Verdict, &status, &item.CandidateCostMillicents, &candidateTTFB,
			&item.BaselineCostMillicents, &baselineTTFB, &item.BaselineOutputText, &item.CandidateOutputText); err != nil {
			return OutboundAIEvaluation{}, mapErr(err)
		}
		if status != nil {
			value := int(*status)
			item.CandidateStatus = &value
		}
		item.CandidateTTFBMS, item.BaselineTTFBMS = widenMS(candidateTTFB), widenMS(baselineTTFB)
		evaluation.Items = append(evaluation.Items, item)
	}
	return evaluation, mapErr(rows.Err())
}

func widenMS(value *int32) *int64 {
	if value == nil {
		return nil
	}
	widened := int64(*value)
	return &widened
}
