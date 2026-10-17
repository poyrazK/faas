package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"strings"
)

// Caller holds the app receipt lock or exclusive graph root lock. All graph
// admissions take that root lock before any member/catalog locks.
func verificationBudgetDB(ctx context.Context, tx pgx.Tx, account string, r verificationRunRecord) (api.BindingVerificationBudget, error) {
	q := sqlc.New()
	root, limit, runs := r.Report.ID, r.Report.MaxConcurrency, []verificationRunRecord{r}
	if r.Report.GraphRunID != "" {
		row, err := q.ReadProjectBindingVerificationRunForBudget(ctx, tx, sqlc.ReadProjectBindingVerificationRunForBudgetParams{ID: mustPgUUID(r.Report.GraphRunID), AccountID: mustPgUUID(account)})
		if err != nil {
			return api.BindingVerificationBudget{}, mapErr(err)
		}
		graph, err := graphVerificationSQL(row)
		if err != nil {
			return api.BindingVerificationBudget{}, err
		}
		root, limit, runs = graph.Report.ID, graph.Report.MaxConcurrency, nil
		for _, name := range graphVerificationMemberNames(graph.Report) {
			member := graph.Report.Members[name]
			row, err := q.ReadBindingVerificationRun(ctx, tx, sqlc.ReadBindingVerificationRunParams{ID: mustPgUUID(member.ID), AccountID: mustPgUUID(account), AppID: mustPgUUID(member.Policy.AppID)})
			if err != nil {
				return api.BindingVerificationBudget{}, mapErr(err)
			}
			child, err := decodeVerificationRun(row)
			if err != nil {
				return api.BindingVerificationBudget{}, err
			}
			if child.Report.GraphRunID != root {
				return api.BindingVerificationBudget{}, ErrConflict
			}
			if err = loadVerificationRunResources(ctx, tx, &child); err != nil {
				return api.BindingVerificationBudget{}, err
			}
			runs = append(runs, child)
		}
	}
	out, refs, err := verificationBudgetSelection(root, limit, runs)
	if err != nil {
		return out, err
	}
	tasks, queues := []pgtype.UUID{}, []pgtype.UUID{}
	for key := range refs {
		kind, id, _ := strings.Cut(key, "/")
		if kind == "task" {
			tasks = append(tasks, mustPgUUID(id))
		} else {
			queues = append(queues, mustPgUUID(id))
		}
	}
	count, err := q.CountUnfinishedVerificationResources(ctx, tx, sqlc.CountUnfinishedVerificationResourcesParams{AccountID: mustPgUUID(account), TaskIds: tasks, QueueIds: queues})
	if err != nil {
		return out, mapErr(err)
	}
	out.ActiveResources = int(count)
	out.Full = out.ActiveResources >= out.MaxConcurrency
	return out, nil
}
func (s *PgStore) BindingVerificationRunBudget(ctx context.Context, account, app, id string) (api.BindingVerificationBudget, error) {
	var out api.BindingVerificationBudget
	err := s.withVerificationRunTx(ctx, account, app, id, false, func(tx pgx.Tx, r *verificationRunRecord) error {
		var err error
		out, err = verificationBudgetDB(ctx, tx, account, *r)
		return err
	})
	return out, err
}
