package managedpostgres

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// This private observation discovers bootstrap SQL pins on an independently
// prepared project. It neither provisions customer databases/globals nor grants
// durable dispatch or dataset readiness. Persist names only in sealed metadata.
type SnapshotCopyTargetSQLObservation struct {
	ProviderResourceID, DataResourceID, EndpointID string
	ProviderCreatedAt, EndpointCreatedAt           time.Time
	Identity                                       SnapshotCopyTargetSQLIdentity
}

func (SnapshotCopyTargetSQLObservation) String() string {
	return "private target PostgreSQL bootstrap observation"
}
func (o SnapshotCopyTargetSQLObservation) GoString() string { return o.String() }
func (SnapshotCopyTargetSQLObservation) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ PrivateTargetSQLObservation bool }{true})
}

type SnapshotCopyTargetSQLInspectionProvider interface {
	// Inspect only the provider's fixed provisioned bootstrap database and role.
	// The provider authenticates SQL and independent topology and closes SQL.
	InspectSnapshotCopyTargetSQL(context.Context, RestoreSourceDefinition, SnapshotCopyTargetRequest) (SnapshotCopyTargetSQLObservation, error)
}

func validateSnapshotCopyTargetSQLInspection(d RestoreSourceDefinition, r SnapshotCopyTargetRequest) error {
	if r.Validate() != nil || r.ExpectedProviderResourceID == "" || r.Capture.Snapshot.SourceResourceID != d.DataResourceID || r.ExpectedProviderResourceID == d.ProviderResourceID {
		return ErrInvalid
	}
	return nil
}

func (o SnapshotCopyTargetSQLObservation) Validate(d RestoreSourceDefinition, r SnapshotCopyTargetRequest) error {
	if err := validateSnapshotCopyTargetSQLInspection(d, r); err != nil {
		return err
	}
	i := o.Identity
	validName := func(v string) bool {
		return v != "" && len(v) <= 63 && utf8.ValidString(v) && !strings.ContainsRune(v, 0)
	}
	if o.ProviderResourceID != r.ExpectedProviderResourceID || !o.ProviderCreatedAt.Equal(r.ExpectedCreatedAt) ||
		!validDataResourceID(o.DataResourceID) || o.DataResourceID == d.ProviderResourceID || o.DataResourceID == d.DataResourceID || o.DataResourceID == r.Capture.ExpectedTargetResourceID || o.DataResourceID == o.ProviderResourceID ||
		!validOpaqueID(o.EndpointID) || o.EndpointID == o.DataResourceID || o.EndpointID == o.ProviderResourceID || o.EndpointID == d.ProviderResourceID || o.EndpointID == d.DataResourceID || o.EndpointID == r.Capture.ExpectedTargetResourceID ||
		o.EndpointCreatedAt.Before(o.ProviderCreatedAt) || o.EndpointCreatedAt.After(time.Now()) || o.EndpointCreatedAt.Nanosecond()%1000 != 0 ||
		i.PostgresMajor != d.Spec.PostgresMajor || !validName(i.DatabaseName) || !validName(i.RoleName) || i.DatabaseOID == 0 || i.RoleOID == 0 {
		return ErrConflict
	}
	return nil
}

// Inspect an already owned target even when new creation/admission is disabled.
// The caller must authenticate fresh durable ownership before this observation.
func (s *Service) InspectSnapshotCopyTargetSQL(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetSQLObservation, error) {
	if err := validateSnapshotCopyTargetSQLInspection(d, r); err != nil {
		return SnapshotCopyTargetSQLObservation{}, err
	}
	b, err := s.checkpointBackend(d)
	if err != nil {
		return SnapshotCopyTargetSQLObservation{}, err
	}
	p, ok := b.Provider.(SnapshotCopyTargetSQLInspectionProvider)
	if !ok {
		return SnapshotCopyTargetSQLObservation{}, ErrUnsupported
	}
	providerCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	actual, err := p.InspectSnapshotCopyTargetSQL(providerCtx, d, r)
	if err != nil {
		return SnapshotCopyTargetSQLObservation{}, err
	}
	if err := actual.Validate(d, r); err != nil {
		return SnapshotCopyTargetSQLObservation{}, err
	}
	if err := providerCtx.Err(); err != nil {
		return SnapshotCopyTargetSQLObservation{}, err
	}
	return actual, nil
}
