package outbound

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// aiCallTags reads an AI call's optional X-Gregale-AI-Tags header
// (ADR-1436): one header of at most OutboundAITagsPerCall key=value tags.
// The gateway never forwards X-Gregale-* headers.
func aiCallTags(r *http.Request, integration Integration) ([]string, bool) {
	if integration.AIProvider == "" {
		return nil, true
	}
	values := r.Header.Values(api.OutboundAITagsHeader)
	switch len(values) {
	case 0:
		return nil, true
	case 1:
		return api.ParseOutboundAITags(values[0])
	}
	return nil, false
}

// tagDailyCostLimits returns the integration's limits on the call's tags,
// or nil.
func tagDailyCostLimits(integration Integration, tags []string) map[string]int64 {
	var limits map[string]int64
	for _, tag := range tags {
		if limit, ok := integration.AITagDailyCostLimits[tag]; ok {
			if limits == nil {
				limits = make(map[string]int64, len(tags))
			}
			limits[tag] = limit
		}
	}
	return limits
}

// aiTagState is one limited tag of a call as admission reads it: its
// limit, its settled UTC-day cost and the live reservations of calls
// carrying it.
type aiTagState struct {
	tag               string
	limit, used, held int64
}

// aiTagBudgetExhausted refuses a call when one of its limited tags cannot
// fit its reservation, like the other cost limits (ADR-991).
func aiTagBudgetExhausted(utcNow time.Time, tags []aiTagState, reserve aiReservation) (string, time.Duration, bool) {
	nextDay := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day()+1, 0, 0, 0, 0, time.UTC)
	for _, tag := range tags {
		if tag.used >= tag.limit || tag.used+tag.held+reserve.cost > tag.limit {
			return ReasonTagDailyCostLimit, retryUntil(utcNow, nextDay), true
		}
	}
	return "", 0, false
}

// tagOverflow is the overflow row of a tag's key.
func tagOverflow(tag string) string {
	return api.OutboundAITagKey(tag) + "=" + api.OutboundAITagOverflowValue
}

// validAITags checks a call's tags as the header parser leaves them.
func validAITags(tags []string) bool {
	if len(tags) > api.OutboundAITagsPerCall || !slices.IsSorted(tags) {
		return false
	}
	for index, tag := range tags {
		if !api.ValidOutboundAITag(tag) || (index > 0 && api.OutboundAITagKey(tags[index-1]) == api.OutboundAITagKey(tag)) {
			return false
		}
	}
	return true
}

// nilIfEmpty stores no tags as NULL.
func nilIfEmpty(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	return tags
}

// formatAITags renders tags as "k=v,k=v" for an OTLP attribute.
func formatAITags(tags []string) string { return strings.Join(tags, ",") }

// queueAITagState reads, for each of the call's tags that has a limit, the
// limit, the tag's settled UTC day and the reservations of live leases
// carrying it. It joins the admission batch, so it adds no round trip.
func queueAITagState(batch *pgx.Batch, integrationID uuid.UUID, today, now time.Time, tags []string) {
	batch.Queue(`
		SELECT tag_limit.tag, tag_limit.daily_cost_limit_millicents,
		       COALESCE((SELECT usage.cost_millicents FROM outbound_ai_tag_usage_daily usage
		                  WHERE usage.integration_id = $1 AND usage.usage_date = $2::date AND usage.tag = tag_limit.tag), 0)::bigint,
		       COALESCE((SELECT sum(lease.ai_reserved_cost_millicents) FROM outbound_admission_leases lease
		                  WHERE lease.integration_id = $1 AND lease.expires_at > $3 AND lease.ai_tags @> ARRAY[tag_limit.tag]), 0)::bigint
		  FROM outbound_ai_tag_limits tag_limit
		 WHERE tag_limit.integration_id = $1 AND tag_limit.tag = ANY($4::text[])
		 ORDER BY tag_limit.tag`, integrationID, today, now, tags)
}

func scanAITagDays(results pgx.BatchResults) ([]aiTagDay, error) {
	rows, err := results.Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var days []aiTagDay
	for rows.Next() {
		var day aiTagDay
		if err := rows.Scan(&day.tag, &day.cost, &day.limit); err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, rows.Err()
}

func scanAITagState(results pgx.BatchResults) ([]aiTagState, error) {
	rows, err := results.Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []aiTagState
	for rows.Next() {
		var tag aiTagState
		if err := rows.Scan(&tag.tag, &tag.limit, &tag.used, &tag.held); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// queueAITagUsageRollup adds a tagged call to each tag's UTC-day row. A new
// tag gets its own row while the integration tracks fewer than
// OutboundAITagsPerDay tags today, or when it has a limit; otherwise it
// adds to its key's overflow row. Each row comes back with its day cost and
// its tag's limit, for budget alerts (ADR-1438).
func queueAITagUsageRollup(batch *pgx.Batch, integrationID uuid.UUID, record AIUsageRecord) {
	var errorRequests int64
	if record.Outcome == AIUsageOutcomeError {
		errorRequests = 1
	}
	overflow := make([]string, 0, len(record.Tags))
	for _, tag := range record.Tags {
		overflow = append(overflow, tagOverflow(tag))
	}
	batch.Queue(`
		INSERT INTO outbound_ai_tag_usage_daily AS usage
		    (integration_id, usage_date, tag, requests, error_requests, input_tokens, output_tokens, cost_millicents)
		SELECT $1, day.today,
		       CASE WHEN EXISTS (SELECT 1 FROM outbound_ai_tag_usage_daily existing
		                          WHERE existing.integration_id = $1 AND existing.usage_date = day.today AND existing.tag = call.tag)
		              OR EXISTS (SELECT 1 FROM outbound_ai_tag_limits tag_limit
		                          WHERE tag_limit.integration_id = $1 AND tag_limit.tag = call.tag)
		              OR (SELECT count(*) FROM outbound_ai_tag_usage_daily existing
		                   WHERE existing.integration_id = $1 AND existing.usage_date = day.today) < $4
		            THEN call.tag ELSE call.overflow END,
		       1, $5, $6, $7, $8
		  FROM (SELECT (now() AT TIME ZONE 'UTC')::date AS today) day,
		       unnest($2::text[], $3::text[]) AS call(tag, overflow)
		ON CONFLICT (integration_id, usage_date, tag) DO UPDATE SET
		    requests = usage.requests + 1,
		    error_requests = usage.error_requests + EXCLUDED.error_requests,
		    input_tokens = usage.input_tokens + EXCLUDED.input_tokens,
		    output_tokens = usage.output_tokens + EXCLUDED.output_tokens,
		    cost_millicents = usage.cost_millicents + EXCLUDED.cost_millicents,
		    updated_at = now()
		RETURNING usage.tag, usage.cost_millicents,
		          (SELECT tag_limit.daily_cost_limit_millicents FROM outbound_ai_tag_limits tag_limit
		            WHERE tag_limit.integration_id = usage.integration_id AND tag_limit.tag = usage.tag)`,
		integrationID, record.Tags, overflow, api.OutboundAITagsPerDay, errorRequests,
		record.Usage.InputTokens, record.Usage.OutputTokens, record.CostMillicents)
}
