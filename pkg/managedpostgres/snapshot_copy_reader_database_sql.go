package managedpostgres

import (
	"context"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// Database must come from the original immutable export plan, never a client
// name or current source catalogue. This private request grants no write access,
// admission projection, data readiness or new reader creation authority.
type SnapshotCopyReaderDatabaseSQLRequest struct {
	Reader   SnapshotCopyReaderRequest
	Database copyinventory.DatabaseExport
}

func (r SnapshotCopyReaderDatabaseSQLRequest) Validate(d RestoreSourceDefinition) error {
	x, scope := r.Database, r.Database.Scope
	hash, err := hex.DecodeString(x.InventoryFingerprint)
	if r.Reader.Validate() != nil || r.Reader.ExpectedEndpointID == "" || scope.Validate() != nil ||
		x.Database.OID == 0 || x.AuthenticatedReaderRoleOID == 0 || x.Database.Name == "" || len(x.Database.Name) > 63 || !utf8.ValidString(x.Database.Name) || strings.ContainsRune(x.Database.Name, 0) ||
		err != nil || len(hash) != 32 || hex.EncodeToString(hash) != x.InventoryFingerprint {
		return ErrInvalid
	}
	if scope.PostgresMajor != d.Spec.PostgresMajor || scope.BackendID != d.BackendID || scope.BackendFingerprint != d.BackendFingerprint ||
		scope.SourceProviderResourceID != d.ProviderResourceID || scope.SourceDataResourceID != d.DataResourceID ||
		scope.SourceDataResourceID != r.Reader.Capture.Snapshot.SourceResourceID || scope.ProviderSnapshotID != r.Reader.Capture.ProviderSnapshotID ||
		scope.CaptureProviderResourceID != r.Reader.Capture.ExpectedTargetResourceID || !scope.CapturePoint.Equal(r.Reader.Capture.Snapshot.PointInTime) ||
		!scope.SnapshotCreatedAt.Equal(r.Reader.SnapshotCreatedAt) || !scope.CaptureCreatedAt.Equal(r.Reader.CaptureCreatedAt) {
		return ErrConflict
	}
	if !x.CapturedAllowConnections {
		return ErrUnsupported
	}
	return nil
}

type SnapshotCopyReaderDatabaseSQLProvider interface {
	// The provider authenticates selected SQL name/OID and the original reader
	// role OID before/after the synchronous borrow, then closes the connection.
	WithSnapshotCopyReaderDatabaseSQL(context.Context, RestoreSourceDefinition, SnapshotCopyReaderDatabaseSQLRequest, SnapshotCopyReaderSQLRead) error
}

func (s *Service) WithSnapshotCopyReaderDatabaseSQL(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderDatabaseSQLRequest, read SnapshotCopyReaderSQLRead) error {
	if err := r.Validate(d); err != nil {
		return err
	}
	return s.withSnapshotCopyReaderSQL(ctx, d, r.Reader, &r.Database, read)
}
