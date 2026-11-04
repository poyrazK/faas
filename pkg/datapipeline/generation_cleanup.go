package datapipeline

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// A failed resync can be followed by another resync before the old generation
// was retired. Recover actual remote ownership instead of assuming generation-1
// is the only generation still holding replication resources.
func (e *Engine) cleanupRetiredGenerations(ctx context.Context, p Pipeline, raw string, source *pgxpool.Pool, target *pgx.Conn, current func(context.Context) error) error {
	retired, err := retiredGenerations(ctx, p, source, target)
	if err != nil {
		return err
	}
	for _, previous := range retired {
		if err = current(ctx); err != nil {
			return err
		}
		if err = e.cleanupGeneration(ctx, previous, raw, source, target); err != nil {
			return err
		}
	}
	return nil
}

func retiredGenerations(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn) ([]Pipeline, error) {
	owned := map[int64]Pipeline{}
	var receipt bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.mirrors') IS NOT NULL`).Scan(&receipt); err != nil {
		return nil, err
	}
	if receipt {
		previous := p
		err := target.QueryRow(ctx, `SELECT generation,tables FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&previous.Generation, &previous.Tables)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			if previous.Generation < 1 || previous.Generation > p.Generation {
				return nil, ErrConflict
			}
			if previous.Generation < p.Generation {
				owned[previous.Generation] = previous
			}
		}
	}
	prefix := strings.TrimSuffix(pipelineName(p), strconv.FormatInt(p.Generation, 10))
	rows, err := source.Query(ctx, `SELECT pubname,coalesce(obj_description(oid,'pg_publication'),'') FROM pg_publication WHERE starts_with(pubname,$1) ORDER BY pubname LIMIT $2`, prefix, api.DataPipelineGenerationsMax+1)
	if err != nil {
		return nil, err
	}
	count := 0
	for rows.Next() {
		var name, comment string
		if err = rows.Scan(&name, &comment); err != nil {
			break
		}
		count++
		if previous, ok := retiredGeneration(p, name); ok {
			if comment != marker(previous) {
				err = &blocked{"source_publication_conflict", false}
				break
			}
			if _, exists := owned[previous.Generation]; !exists {
				owned[previous.Generation] = previous
			}
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
	// Subscription comments live in the owning database's pg_description even
	// though pg_subscription itself is a shared catalog.
	rows, err = target.Query(ctx, `SELECT subname,coalesce(subslotname,''),subpublications,coalesce(obj_description(oid,'pg_subscription'),'')
 FROM pg_subscription WHERE subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND starts_with(subname,$1) ORDER BY subname LIMIT $2`, prefix, api.DataPipelineGenerationsMax+1)
	if err != nil {
		return nil, err
	}
	count = 0
	for rows.Next() {
		var name, slot, comment string
		var publications []string
		if err = rows.Scan(&name, &slot, &publications, &comment); err != nil {
			break
		}
		count++
		if previous, ok := retiredGeneration(p, name); ok {
			_, backed := owned[previous.Generation]
			// A lost CREATE reply can precede COMMENT. Its publication or private
			// receipt must still establish ownership before adopting that result.
			ownedComment := comment == marker(previous) || comment == "" && backed
			if slot != name || !slices.Equal(publications, []string{name}) || !ownedComment {
				err = &blocked{"destination_subscription_conflict", false}
				break
			}
			if !backed {
				owned[previous.Generation] = previous
			}
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
	result := make([]Pipeline, 0, len(owned))
	for _, previous := range owned {
		result = append(result, previous)
	}
	slices.SortFunc(result, func(a, b Pipeline) int { return cmp.Compare(a.Generation, b.Generation) })
	return result, nil
}

func retiredGeneration(p Pipeline, name string) (Pipeline, bool) {
	previous, ok := namedGeneration(p, name)
	return previous, ok && previous.Generation < p.Generation
}

func namedGeneration(p Pipeline, name string) (Pipeline, bool) {
	prefix := strings.TrimSuffix(pipelineName(p), strconv.FormatInt(p.Generation, 10))
	value, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return Pipeline{}, false
	}
	generation, err := strconv.ParseInt(value, 10, 64)
	if err != nil || generation < 1 || generation > api.DataPipelineGenerationsMax {
		return Pipeline{}, false
	}
	p.Generation = generation
	return p, pipelineName(p) == name
}
