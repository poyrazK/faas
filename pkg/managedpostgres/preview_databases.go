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
	return "the account holds as many PR preview databases as its plan allows; close a pull request or tear its preview down first"
}

func (previewDatabaseLimitError) Unwrap() error { return ErrQuotaExceeded }

// errPreviewCopyUnavailable refuses copy until it is delivered
// (ADR-20261102123523 delivery 5).
var errPreviewCopyUnavailable = fmt.Errorf("%w: copy previews are not available yet; use empty", ErrInvalid)

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
	case PreviewsOff, PreviewsEmpty:
	case PreviewsCopy:
		return Database{}, errPreviewCopyUnavailable
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
	switch source.Previews {
	case PreviewsEmpty:
	case PreviewsCopy:
		return Database{}, errPreviewCopyUnavailable
	default:
		return Database{}, ErrPreviewsOff
	}
	spec := Spec{Region: source.Spec.Region, PostgresMajor: source.Spec.PostgresMajor, Class: ClassDevelopment,
		Availability: AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: request.StorageLimitBytes}
	if err := spec.Validate(); err != nil {
		return Database{}, err
	}
	backend, err := s.registry.Default(spec.Region)
	if err != nil {
		return Database{}, err
	}
	if err := backend.Capabilities.SupportsNewDatabase(spec); err != nil {
		return Database{}, err
	}
	if s.admit != nil {
		if err := s.admit(ctx, PolicySubject(ctx, request.AccountID)); err != nil {
			return Database{}, err
		}
	}
	if err := s.admitFleet(ctx, PolicySubject(ctx, request.AccountID), true); err != nil {
		return Database{}, err
	}
	now := s.now()
	database, _, err := store.ReservePreview(ctx, Database{
		ID:                 s.newID(),
		AccountID:          request.AccountID,
		OrgID:              Organization(ctx),
		Name:               PreviewDatabaseName(source.Name, request.Owner),
		Spec:               spec,
		BackendID:          backend.ID,
		BackendFingerprint: backend.Fingerprint,
		State:              StateProvisioning,
		DesiredGeneration:  1,
		CreatedAt:          now,
		UpdatedAt:          now,
		Preview:            request.Owner,
	}, request.Limit)
	if err != nil {
		return Database{}, err
	}
	return s.previewDatabaseProgress(ctx, database)
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
