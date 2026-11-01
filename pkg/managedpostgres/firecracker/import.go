package firecracker

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Importing a database from another backend (ADR-1027 migration off Neon).
// The database's own VM copies the source into its empty customer
// database, restoring every object as the schema owner, so the usual
// credentials reach the copy like anything a migration created. The source
// endpoint never reaches the VM: its node relays the copy over TLS it
// verifies, to public addresses only.

var _ managedpostgres.Importer = (*Provider)(nil)

// Import starts copying the source into the database under
// req.OperationID, waking it. A running or finished import under that
// operation is reported, not restarted, and one under another operation is
// refused with ErrConflict; the target records a finished import's
// operation, so this holds across VM restarts.
func (p *Provider) Import(ctx context.Context, req managedpostgres.ImportRequest) (managedpostgres.ImportStatus, error) {
	src := req.Source
	if req.ResourceID == "" || src.Host == "" || src.Port < 1 || src.Port > 65535 || !dbguest.ValidOperationID(req.OperationID) {
		return managedpostgres.ImportStatus{}, managedpostgres.ErrInvalid
	}
	progress, err := p.scheduler.ImportDatabase(ctx, &scheddpb.ImportDatabaseRequest{
		DatabaseId: resourceUUID(req.ResourceID), Host: src.Host, Port: int32(src.Port), //nolint:gosec // port checked
		SourceDatabase: src.Database, User: src.User, Password: src.Password,
		TargetDatabase: p.cfg.Database, Owner: schemaOwner, SkipExtensions: src.SkipExtensions,
		OperationId: req.OperationID,
	})
	if err != nil {
		return managedpostgres.ImportStatus{}, providerError(err)
	}
	return importStatus(progress), nil
}

// ImportStatus reports the database's import without waking it: a
// database whose VM stopped since reports none. An unfinished import left
// the database empty again; a finished one is reported by Import under its
// operation.
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
	out := managedpostgres.ImportStatus{State: managedpostgres.ImportState(in.GetState()), ErrorCode: in.GetErrorCode(), SourceMajor: int(in.GetSourceMajor()),
		OperationID: in.GetOperationId(), MissingExtensions: dbguest.ReportableExtensions(in.GetMissingExtensions()),
		RequiredVolumeBytes: max(in.GetRequiredVolumeBytes(), 0)}
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
