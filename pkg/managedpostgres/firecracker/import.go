package firecracker

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Importing a database from another backend (ADR-1027 migration off Neon).
// The database's own VM copies the source into its empty customer
// database, restoring every object as the schema owner, so the usual
// credentials reach the copy like anything a migration created. The source
// endpoint never reaches the VM: its node relays the copy over TLS it
// verifies, to public addresses only.

var _ managedpostgres.Importer = (*Provider)(nil)

// Import starts copying the source into the database, waking it. A
// running or finished import is reported, not restarted.
func (p *Provider) Import(ctx context.Context, req managedpostgres.ImportRequest) (managedpostgres.ImportStatus, error) {
	src := req.Source
	if req.ResourceID == "" || src.Host == "" || src.Port < 1 || src.Port > 65535 {
		return managedpostgres.ImportStatus{}, managedpostgres.ErrInvalid
	}
	progress, err := p.scheduler.ImportDatabase(ctx, &scheddpb.ImportDatabaseRequest{
		DatabaseId: resourceUUID(req.ResourceID), Host: src.Host, Port: int32(src.Port), //nolint:gosec // port checked
		SourceDatabase: src.Database, User: src.User, Password: src.Password,
		TargetDatabase: p.cfg.Database, Owner: schemaOwner, SkipExtensions: src.SkipExtensions,
	})
	if err != nil {
		return managedpostgres.ImportStatus{}, providerError(err)
	}
	return importStatus(progress), nil
}

// ImportStatus reports the database's import; one whose VM stopped since
// reports none, with the database empty again.
func (p *Provider) ImportStatus(ctx context.Context, resourceID string) (managedpostgres.ImportStatus, error) {
	if resourceID == "" {
		return managedpostgres.ImportStatus{}, managedpostgres.ErrInvalid
	}
	progress, err := p.scheduler.DatabaseImportStatus(ctx, &scheddpb.DatabaseRef{DatabaseId: resourceUUID(resourceID)})
	if err != nil {
		return managedpostgres.ImportStatus{}, providerError(err)
	}
	return importStatus(progress), nil
}

func importStatus(in *scheddpb.DatabaseImportProgress) managedpostgres.ImportStatus {
	out := managedpostgres.ImportStatus{State: managedpostgres.ImportState(in.GetState()), ErrorCode: in.GetErrorCode(), SourceMajor: int(in.GetSourceMajor())}
	if out.State == "" {
		out.State = managedpostgres.ImportNone
	}
	if in.GetStartedAt() != nil {
		out.StartedAt = in.GetStartedAt().AsTime()
	}
	if in.GetFinishedAt() != nil {
		out.FinishedAt = in.GetFinishedAt().AsTime()
	}
	return out
}
