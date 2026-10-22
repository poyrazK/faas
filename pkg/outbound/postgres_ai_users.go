package outbound

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// queueAIUserState reads one end user's settled UTC day and the
// reservations of its live leases (ADR-994). Admission holds the
// integration's admission-state row lock, so admissions for the same end
// user are serialized. A user without a row today is checked against the
// day's tracking cap (ADR-996); the count runs only for such a user.
func queueAIUserState(batch *pgx.Batch, integrationID uuid.UUID, today time.Time, endUser string) {
	batch.Queue(`
		SELECT COALESCE(user_day.tokens, 0)::bigint, COALESCE(user_day.cost, 0)::bigint,
		       COALESCE(sum(lease.ai_reserved_tokens), 0)::bigint, COALESCE(sum(lease.ai_reserved_cost_millicents), 0)::bigint,
		       CASE WHEN user_day.tokens IS NOT NULL THEN 0
		            ELSE (SELECT count(*) FROM outbound_ai_user_usage_daily
		                   WHERE integration_id = $1 AND usage_date = $2::date) END
		  FROM (SELECT (SELECT input_tokens + output_tokens FROM outbound_ai_user_usage_daily
		                 WHERE integration_id = $1 AND usage_date = $2::date AND end_user = $3) AS tokens,
		               (SELECT cost_millicents FROM outbound_ai_user_usage_daily
		                 WHERE integration_id = $1 AND usage_date = $2::date AND end_user = $3) AS cost) user_day
		  LEFT JOIN outbound_admission_leases lease ON lease.integration_id = $1 AND lease.ai_end_user = $3
		 GROUP BY user_day.tokens, user_day.cost`, integrationID, today, endUser)
}

func scanAIUserState(results pgx.BatchResults) (aiBudgetState, error) {
	var state aiBudgetState
	var users int64
	err := results.QueryRow().Scan(&state.userTokens, &state.userCost, &state.inflightUserTokens, &state.inflightUserCost, &users)
	state.userUntracked = users >= api.OutboundAIUsersPerDay
	return state, err
}

// queueAIUserUsageRollup adds a call that names an end user to that user's
// UTC-day row. A new user may only add a row while the day has fewer than
// the capped number of distinct users; later ones aggregate under the
// overflow row, so a caller cannot grow the table without bound. The row
// the call landed in, with its day totals, is read back for budget alerts
// (ADR-995).
func queueAIUserUsageRollup(batch *pgx.Batch, integrationID uuid.UUID, record AIUsageRecord) {
	var errorRequests int64
	if record.Outcome == AIUsageOutcomeError {
		errorRequests = 1
	}
	batch.Queue(`
		INSERT INTO outbound_ai_user_usage_daily AS usage
		    (integration_id, usage_date, end_user, requests, error_requests, input_tokens, output_tokens, cost_millicents)
		SELECT $1, day.today,
		       CASE WHEN EXISTS (SELECT 1 FROM outbound_ai_user_usage_daily existing
		                          WHERE existing.integration_id = $1 AND existing.usage_date = day.today
		                            AND existing.end_user = $2)
		              OR (SELECT count(*) FROM outbound_ai_user_usage_daily existing
		                   WHERE existing.integration_id = $1 AND existing.usage_date = day.today) < $3
		            THEN $2 ELSE $4 END,
		       1, $5, $6, $7, $8
		  FROM (SELECT (now() AT TIME ZONE 'UTC')::date AS today) day
		ON CONFLICT (integration_id, usage_date, end_user) DO UPDATE SET
		    requests = usage.requests + 1,
		    error_requests = usage.error_requests + EXCLUDED.error_requests,
		    input_tokens = usage.input_tokens + EXCLUDED.input_tokens,
		    output_tokens = usage.output_tokens + EXCLUDED.output_tokens,
		    cost_millicents = usage.cost_millicents + EXCLUDED.cost_millicents,
		    updated_at = now()
		RETURNING end_user, input_tokens + output_tokens, cost_millicents`,
		integrationID, record.EndUser, api.OutboundAIUsersPerDay, api.OutboundAIUserOverflow, errorRequests,
		record.Usage.InputTokens, record.Usage.OutputTokens, record.CostMillicents)
}

// aiUserDay is the per-user rollup row a settlement landed in.
type aiUserDay struct {
	endUser      string
	tokens, cost int64
}
