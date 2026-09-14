package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// validatePreviewArchivePlanLimit mirrors the upload-size admission gate for
// an explicit archive before a dry run reports success. Archive structure was
// already validated by materializeDeployArchive against the same snapshot.
func validatePreviewArchivePlanLimit(ctx context.Context, client *Client, path string) error {
	file, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		return fmt.Errorf("stat source archive: %w", statErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close source archive: %w", closeErr)
	}
	// Every public plan accepts the Free-plan allowance. Avoid adding an
	// account round trip to ordinary previews; only archives whose size needs
	// plan-specific admission require Whoami.
	minimumCapBytes := int64(api.MustLimitsFor(api.PlanFree).SourceTarballMaxMB) * 1024 * 1024
	if info.Size() <= minimumCapBytes {
		return nil
	}
	account, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("resolve source limit: %w", err)
	}
	limits, ok := api.LimitsFor(api.Plan(account.Plan))
	if !ok {
		return fmt.Errorf("resolve source limit: unknown account plan %q", account.Plan)
	}
	capBytes := int64(limits.SourceTarballMaxMB) * 1024 * 1024
	if info.Size() > capBytes {
		problem := api.ErrSourceTooLarge(limits, info.Size())
		return &api.APIError{Problem: *problem}
	}
	return nil
}
