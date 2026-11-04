package datapipeline

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func mirrorGeneration(ctx context.Context, p Pipeline, target receiptSQL) (int64, error) {
	var present bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.mirrors') IS NOT NULL`).Scan(&present); err != nil || !present {
		return 0, err
	}
	var generation int64
	err := target.QueryRow(ctx, `SELECT generation FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return generation, err
}

func ownedSubscription(p Pipeline, slot string, publications []string, comment string, backed bool) bool {
	name := pipelineName(p)
	return slot == name && slices.Equal(publications, []string{name}) && (comment == marker(p) || comment == "" && backed)
}

func checkSubscriptionOwnership(ctx context.Context, p Pipeline, target receiptSQL, backed bool) (bool, error) {
	var slot, comment string
	var publications []string
	err := target.QueryRow(ctx, `SELECT coalesce(subslotname,''),subpublications,coalesce(obj_description(oid,'pg_subscription'),'')
 FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())`, pipelineName(p)).Scan(&slot, &publications, &comment)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !ownedSubscription(p, slot, publications, comment, backed) {
		return false, &blocked{"destination_subscription_conflict", false}
	}
	return true, nil
}

// Inspect every generation before changing any subscription. Source access is
// optional: an exact marker or matching private receipt can establish ownership
// even when a new source login fails while native sessions are still applying.
func pipelineSubscriptions(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn) ([]Pipeline, error) {
	generation, err := mirrorGeneration(ctx, p, target)
	if err != nil {
		return nil, err
	}
	if generation > p.Generation {
		return nil, ErrConflict
	}
	prefix := strings.TrimSuffix(pipelineName(p), strconv.FormatInt(p.Generation, 10))
	rows, err := target.Query(ctx, `SELECT subname,coalesce(subslotname,''),subpublications,coalesce(obj_description(oid,'pg_subscription'),'')
 FROM pg_subscription WHERE subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND starts_with(subname,$1)
 ORDER BY subname LIMIT $2`, prefix, api.DataPipelineGenerationsMax+1)
	if err != nil {
		return nil, err
	}
	type subscription struct {
		pipeline      Pipeline
		slot, comment string
		publications  []string
	}
	var pending []subscription
	count := 0
	for rows.Next() {
		var name string
		var s subscription
		if err = rows.Scan(&name, &s.slot, &s.publications, &s.comment); err != nil {
			break
		}
		count++
		if candidate, ok := namedGeneration(p, name); ok {
			s.pipeline = candidate
			pending = append(pending, s)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if count > api.DataPipelineGenerationsMax {
		return nil, ErrConflict
	}
	owned := make([]Pipeline, 0, len(pending))
	for _, s := range pending {
		if s.pipeline.Generation > p.Generation {
			return nil, ErrConflict
		}
		backed := generation == s.pipeline.Generation
		if s.comment == "" && !backed && source != nil {
			if err = source.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_publication WHERE pubname=$1 AND coalesce(obj_description(oid,'pg_publication'),'')=$2)`, pipelineName(s.pipeline), marker(s.pipeline)).Scan(&backed); err != nil {
				return nil, err
			}
		}
		if !ownedSubscription(s.pipeline, s.slot, s.publications, s.comment, backed) {
			return nil, &blocked{"destination_subscription_conflict", false}
		}
		owned = append(owned, s.pipeline)
	}
	return owned, nil
}

func pausePipelineSubscriptions(ctx context.Context, p Pipeline, target *pgx.Conn, current func(context.Context) error) error {
	owned, err := pipelineSubscriptions(ctx, p, nil, target)
	if err != nil {
		return err
	}
	for _, candidate := range owned {
		if err = current(ctx); err != nil {
			return err
		}
		if err = alterSubscriptionEnabled(ctx, candidate, target, false); err != nil {
			return err
		}
	}
	return nil
}
