// Package checkpoint defines private connection-selection requests without
// depending on the managed database service or the control-plane state store.
package checkpoint

import (
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// The barrier owner is operation-specific; the maintenance owner has its own
// stable private identity and may be reused by successive clone operations.
type CheckpointConnectionIdentity struct {
	OwnerToken, SourceResourceID string
}

func (i CheckpointConnectionIdentity) Validate() error {
	token, err := uuid.Parse(i.OwnerToken)
	if err != nil || token == uuid.Nil || token.String() != i.OwnerToken ||
		i.SourceResourceID == "" || len(i.SourceResourceID) > 255 || !utf8.ValidString(i.SourceResourceID) {
		return pgerrors.ErrInvalid
	}
	for _, character := range i.SourceResourceID {
		if character < 0x20 || character == 0x7f {
			return pgerrors.ErrInvalid
		}
	}
	return nil
}

// The caller must durably own the exact source and selected database set before
// dispatch. Names alone are not evidence of complete cluster/writer coverage.
// Retries use the same operation owner and set; unknown replies retain the hold.
type CheckpointConnectionRequest struct {
	CheckpointConnectionIdentity
	DatabaseNames []string
}

func (CheckpointConnectionRequest) String() string {
	return "[private PostgreSQL checkpoint selection]"
}
func (r CheckpointConnectionRequest) GoString() string { return r.String() }
func (CheckpointConnectionRequest) MarshalJSON() ([]byte, error) {
	return []byte(`"[private PostgreSQL checkpoint selection]"`), nil
}

func (r CheckpointConnectionRequest) Validate() error {
	if r.CheckpointConnectionIdentity.Validate() != nil || len(r.DatabaseNames) == 0 || len(r.DatabaseNames) > api.PostgresCheckpointDatabasesMax {
		return pgerrors.ErrInvalid
	}
	names := make(map[string]bool, len(r.DatabaseNames))
	for _, name := range r.DatabaseNames {
		if name == "" || len(name) > 63 || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') || names[name] {
			return pgerrors.ErrInvalid
		}
		names[name] = true
	}
	return nil
}
