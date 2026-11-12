package managedpostgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PR preview databases (ADR-20261102123523). A production database's previews
// setting gives each PR preview environment a database of its own, which apid
// binds to the environment's previews and removes with them.

// PreviewMode is what a database gives PR preview environments.
type PreviewMode string

const (
	PreviewsOff   PreviewMode = "off"
	PreviewsEmpty PreviewMode = "empty"
	PreviewsCopy  PreviewMode = "copy"
)

// PreviewOwner names the PR preview environment that owns a database: its
// source database, the environment (a project, or a legacy preview's
// production app) and its pull request.
type PreviewOwner struct {
	SourceDatabaseID string
	EnvironmentID    string
	PRNumber         int
}

// IsZero reports whether no preview environment owns the database.
func (o PreviewOwner) IsZero() bool { return o == PreviewOwner{} }

var previewEnvironmentID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func (o PreviewOwner) valid() bool {
	return o.SourceDatabaseID != "" && previewEnvironmentID.MatchString(o.EnvironmentID) && o.PRNumber > 0
}

// ErrPreviewDatabase refuses a change a PR preview environment's database
// cannot take: previews of its own, a restore from it, or a binding apid did
// not set up. It is an ErrConflict.
var ErrPreviewDatabase = fmt.Errorf("%w: a PR preview environment owns this database", ErrConflict)

// ErrPreviewsOff refuses a preview database for a source whose previews
// setting is off. It is an ErrConflict.
var ErrPreviewsOff = fmt.Errorf("%w: the database's previews setting is off", ErrConflict)

// ErrPreviewDatabaseLimit refuses a preview database past the owner's
// allowance. It unwraps to ErrQuotaExceeded.
var ErrPreviewDatabaseLimit error = previewDatabaseLimitError{}

type previewDatabaseLimitError struct{}

func (previewDatabaseLimitError) Error() string {
	return "the account holds as many PR preview databases as it may (its plan's allowance or its share of the platform); close a pull request or tear its preview down first"
}

func (previewDatabaseLimitError) Unwrap() error { return ErrQuotaExceeded }

// ErrPreviewCopyUnsupported refuses copy previews of a database that cannot
// be restored to a point in time: one without a restore window, or on a
// backend that cannot restore. It is an ErrInvalid.
var ErrPreviewCopyUnsupported = fmt.Errorf("%w: copy previews need the database's restore window and a backend that restores to a point in time", ErrInvalid)

// PreviewDatabaseStore is an optional Store capability. ReservePreview
// reserves the database database.Preview names, or returns the undeleted one
// that environment already owns, under the owner's reservation lock; it
// refuses ErrPreviewDatabaseLimit when the owner holds limit preview
// databases, and ErrConflict when a database of another owner has the name.
type PreviewDatabaseStore interface {
	SetPreviews(ctx context.Context, accountID, databaseID string, mode PreviewMode, at time.Time) (Database, error)
	FindPreviewDatabase(ctx context.Context, owner PreviewOwner) (Database, error)
	ReservePreview(ctx context.Context, database Database, limit int) (Database, bool, error)
	ListPreviewDatabases(ctx context.Context, accountID, environmentID string, prNumber int) ([]Database, error)
	// ListPreviewDatabasePage returns up to limit undeleted preview
	// databases across accounts, in ID order after afterID ("" for the
	// first page).
	ListPreviewDatabasePage(ctx context.Context, afterID string, limit int) ([]Database, error)
}

// PreviewDatabaseName is pr-<N>-<source name>-<6 hex characters of the
// environment ID>, the source name shortened to fit ValidName. The suffix
// keeps two projects that preview the same database apart.
func PreviewDatabaseName(sourceName string, owner PreviewOwner) string {
	sum := sha256.Sum256([]byte(owner.EnvironmentID))
	prefix, suffix := "pr-"+strconv.Itoa(owner.PRNumber)+"-", "-"+hex.EncodeToString(sum[:3])
	if room := 63 - len(prefix) - len(suffix); len(sourceName) > room {
		sourceName = strings.TrimRight(sourceName[:room], "-")
	}
	return prefix + sourceName + suffix
}

// PreviewDatabaseRequest asks for the database a PR preview environment gets
// for one source. apid reads the plan: StorageLimitBytes is
// api.ManagedPostgresPreviewStorageLimitBytes or the plan's pool, whichever is
// smaller, and Limit is api.ManagedPostgresPreviewDatabasesMax.
type PreviewDatabaseRequest struct {
	AccountID         string
	Owner             PreviewOwner
	StorageLimitBytes int64
	Limit             int
}

// SetPreviews changes what a database gives PR preview environments. It
// applies to environments set up afterwards; existing preview databases stay
// until their environment is torn down.
func (s *Service) SetPreviews(ctx context.Context, accountID, databaseID string, mode PreviewMode) (Database, error) {
	store, ok := s.store.(PreviewDatabaseStore)
	if !ok {
		return Database{}, ErrUnavailable
	}
	switch mode {
	case PreviewsOff, PreviewsEmpty, PreviewsCopy:
	default:
		return Database{}, fmt.Errorf("%w: previews must be off, empty or copy", ErrInvalid)
	}
	database, err := customerDatabase(ctx, s.store, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	if !database.Preview.IsZero() {
		return Database{}, ErrPreviewDatabase
	}
	if database.State == StateDeleting || database.State == StateDeleted {
		return Database{}, ErrConflict
	}
	if mode == PreviewsCopy {
		if err := s.previewCopyAllowed(database); err != nil {
			return Database{}, err
		}
	}
	return store.SetPreviews(ctx, database.AccountID, database.ID, mode, s.now())
}

// EnsurePreviewDatabase returns the database request.Owner's environment has
// for its source, setting it up when there is none. An existing one is
// returned as it is: a setting or plan change applies to the next
// environment. A database still provisioning is returned provisioning.
func (s *Service) EnsurePreviewDatabase(ctx context.Context, request PreviewDatabaseRequest) (Database, error) {
	store, ok := s.store.(PreviewDatabaseStore)
	if !ok {
		return Database{}, ErrUnavailable
	}
	if request.AccountID == "" || !request.Owner.valid() || request.StorageLimitBytes <= 0 || request.Limit < 0 {
		return Database{}, ErrInvalid
	}
	existing, err := store.FindPreviewDatabase(ctx, request.Owner)
	if err == nil {
		return s.previewDatabaseProgress(ctx, existing)
	}
	if !errors.Is(err, ErrNotFound) {
		return Database{}, err
	}
	if !s.provisioningEnabled() || !s.provisioningAllowed(ctx, request.AccountID) {
		return Database{}, ErrProvisioningClosed
	}
	source, err := customerDatabase(ctx, s.store, request.AccountID, request.Owner.SourceDatabaseID)
	if err != nil {
		return Database{}, err
	}
	if !source.Preview.IsZero() {
		return Database{}, ErrPreviewDatabase
	}
	if source.State == StateDeleting || source.State == StateDeleted {
		return Database{}, ErrConflict
	}
	now := s.now()
	var reservation Database
	switch source.Previews {
	case PreviewsEmpty:
		reservation, err = s.emptyPreview(source, request.StorageLimitBytes)
	case PreviewsCopy:
		reservation, err = s.copyPreview(source, now)
	default:
		return Database{}, ErrPreviewsOff
	}
	if err != nil {
		return Database{}, err
	}
	if s.admit != nil {
		if err := s.admit(ctx, PolicySubject(ctx, request.AccountID)); err != nil {
			return Database{}, err
		}
	}
	if err := s.admitPreviewFleet(ctx, PolicySubject(ctx, request.AccountID)); err != nil {
		return Database{}, err
	}
	reservation.ID, reservation.AccountID, reservation.OrgID = s.newID(), request.AccountID, Organization(ctx)
	reservation.Name, reservation.Preview = PreviewDatabaseName(source.Name, request.Owner), request.Owner
	reservation.State, reservation.DesiredGeneration, reservation.CreatedAt, reservation.UpdatedAt = StateProvisioning, 1, now, now
	database, _, err := store.ReservePreview(ctx, reservation, s.previewLimit(request.Limit))
	if err != nil {
		return Database{}, err
	}
	return s.previewDatabaseProgress(ctx, database)
}

// emptyPreview is an empty preview database's spec and backend: the
// developer session's, in the source's region and major.
func (s *Service) emptyPreview(source Database, storageLimitBytes int64) (Database, error) {
	spec := Spec{Region: source.Spec.Region, PostgresMajor: source.Spec.PostgresMajor, Class: ClassDevelopment,
		Availability: AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: storageLimitBytes}
	if err := spec.Validate(); err != nil {
		return Database{}, err
	}
	// The source's backend first: the preview takes the production bindings'
	// access modes, which the region's default backend may not offer (the
	// default can change after the source was created).
	if backend, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint); err == nil &&
		backend.Capabilities.SupportsNewDatabase(spec) == nil {
		return Database{Spec: spec, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint}, nil
	}
	backend, err := s.registry.Default(spec.Region)
	if err != nil {
		return Database{}, err
	}
	if err := backend.Capabilities.SupportsNewDatabase(spec); err != nil {
		return Database{}, err
	}
	return Database{Spec: spec, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint}, nil
}

// copyPreview is a copy's restore of source as of a second ago, on the
// source's backend, so a Neon copy is a branch of its source's project. It
// keeps the source's storage limit and restore window, which a Neon branch
// shares with its project, and runs development class, scale-to-zero.
func (s *Service) copyPreview(source Database, now time.Time) (Database, error) {
	if source.State != StateReady || source.ProviderResourceID == "" {
		// The source is changing; the next attempt copies it.
		return Database{}, ErrUnavailable
	}
	if err := s.previewCopyAllowed(source); err != nil {
		return Database{}, err
	}
	// PostgreSQL keeps microseconds; reservations compare the point as stored.
	point := now.Add(-time.Second).UTC().Truncate(time.Microsecond)
	if point.Before(source.CreatedAt) {
		return Database{}, ErrUnavailable
	}
	spec := source.Spec
	spec.Class, spec.Availability, spec.ScaleToZero = ClassDevelopment, AvailabilitySingleZone, true
	backend, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return Database{}, err
	}
	if err := backend.Capabilities.Supports(spec); err != nil {
		return Database{}, err
	}
	return Database{Spec: spec, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		RestoreSourceDatabaseID: source.ID, RestoreSourceResourceID: databaseDataResource(source), RestorePointInTime: point}, nil
}

// previewCopyAllowed reports whether source can be copied for previews: it
// has a restore window, on a backend that restores to a point in time and
// meters a restore apart from, or with, its source.
func (s *Service) previewCopyAllowed(source Database) error {
	if source.Spec.RestoreWindowSeconds <= 0 {
		return ErrPreviewCopyUnsupported
	}
	backend, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return err
	}
	if !backend.Capabilities.PointInTimeRestore ||
		s.registry.UsagePolicy().Enabled && !backend.Capabilities.RestoreUsageIsolated && !backend.Capabilities.RestoreUsageIncludedInSource {
		return ErrPreviewCopyUnsupported
	}
	return nil
}

// previewDatabaseProgress moves a preview database's provisioning along; a
// ready or deleting one is returned as it is. One another worker holds, or
// whose next attempt is not due, is returned as it is too: the lifecycle
// reconciler moves it along.
func (s *Service) previewDatabaseProgress(ctx context.Context, database Database) (Database, error) {
	if database.State != StateProvisioning && database.State != StateFailed {
		return database, nil
	}
	reconciled, err := s.Reconcile(ctx, database.AccountID, database.ID)
	if errors.Is(err, ErrConflict) {
		return database, nil
	}
	return reconciled, err
}

// PreviewDatabases returns the undeleted databases one PR preview environment
// owns.
func (s *Service) PreviewDatabases(ctx context.Context, accountID, environmentID string, prNumber int) ([]Database, error) {
	store, ok := s.store.(PreviewDatabaseStore)
	if !ok {
		return nil, nil
	}
	if accountID == "" || !previewEnvironmentID.MatchString(environmentID) || prNumber <= 0 {
		return nil, ErrInvalid
	}
	return store.ListPreviewDatabases(ctx, accountID, environmentID, prNumber)
}

// PreviewDatabasePage returns up to limit undeleted preview databases across
// accounts, in ID order after afterID ("" for the first page), for the sweep
// that purges those whose PR environment closed.
func (s *Service) PreviewDatabasePage(ctx context.Context, afterID string, limit int) ([]Database, error) {
	store, ok := s.store.(PreviewDatabaseStore)
	if !ok {
		return nil, nil
	}
	if limit <= 0 {
		return nil, ErrInvalid
	}
	return store.ListPreviewDatabasePage(ctx, afterID, limit)
}

// DeletePreviewDatabase deletes and purges a PR preview environment's
// database at once, with no undo window: it is disposable, and a copy holds
// production data that should not outlive the review. Its bindings must be
// gone first, as for any delete.
func (s *Service) DeletePreviewDatabase(ctx context.Context, accountID, databaseID string) (Database, error) {
	database, err := s.store.Get(ctx, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	if database.Preview.IsZero() {
		return Database{}, ErrConflict
	}
	return s.delete(ctx, database)
}
