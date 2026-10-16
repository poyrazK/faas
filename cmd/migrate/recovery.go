package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
)

func runLedgerRecovery(ctx context.Context, pool *pgxpool.Pool, options migrationOptions, output io.Writer) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	switch options.Recovery {
	case "prepare":
		if err := db.PrepareApplicationStandardLedgerRecovery(ctx, pool); err != nil {
			return err
		}
		return encoder.Encode(struct {
			Prepared bool `json:"prepared"`
		}{true})
	case "preview":
		plan, err := db.PreviewApplicationStandardLedgerRecovery(ctx, pool)
		if err != nil {
			return err
		}
		return encoder.Encode(plan)
	case "apply":
		receipt, err := db.ApplyApplicationStandardLedgerRecovery(ctx, pool, options.Approval)
		if err != nil {
			return err
		}
		return encoder.Encode(receipt)
	default:
		return nil
	}
}
