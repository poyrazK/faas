package globaldirectory

// ADR-480: immutable global release intent and durable regional outcomes.
import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrSuperseded = errors.New("global image release superseded")
var ErrNoTargets = errors.New("global image release requires selected targets")
var ErrReleaseLimit = errors.New("global image release target or generation bound reached")

// ReleaseLimitError carries the values observed while holding the app lock.
type ReleaseLimitError struct {
	Kind     string
	Limit    int64
	Observed int64
}

func (e *ReleaseLimitError) Error() string { return ErrReleaseLimit.Error() }
func (e *ReleaseLimitError) Unwrap() error { return ErrReleaseLimit }

func releaseLimitFromDetail(detail string) error {
	fields := strings.Split(detail, ":")
	if len(fields) != 3 {
		return ErrConflict
	}
	expected := int64(api.GlobalImageReleaseTargetsMax)
	switch fields[0] {
	case "targets":
	case "generation":
		expected = api.GlobalImageReleaseGenerationMax
	default:
		return ErrConflict
	}
	limit, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || limit != expected {
		return ErrConflict
	}
	observed, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || observed <= limit {
		return ErrConflict
	}
	return &ReleaseLimitError{Kind: fields[0], Limit: limit, Observed: observed}
}

const (
	ReleasePending        = "pending"
	ReleaseAccepted       = "accepted"
	ReleaseExpired        = "expired"
	ReleaseBlocked        = "blocked"
	ReleaseSuperseded     = "superseded"
	DiagnosticUnavailable = "regional_unavailable"
	DiagnosticRejected    = "regional_rejected"
	DiagnosticIdentity    = "identity_changed"
	DiagnosticReceipt     = "receipt_invalid"
)

type Release struct {
	GlobalAppID       string
	OwnerID           string
	ReleaseID         string
	Generation        int64
	DesiredGeneration int64
	Image             string
	CreatedAt         time.Time
	Targets           []ReleaseTarget
}

type ReleaseTarget struct {
	Target
	Outcome      string
	Diagnostic   string
	DeploymentID string
	UpdatedAt    time.Time
}

type ReleaseResult struct {
	Outcome      string
	Diagnostic   string
	DeploymentID string
}

type ReleaseStore interface {
	PlanRelease(context.Context, string, string, string, string) (Release, error)
	GetRelease(context.Context, string, string, string) (Release, error)
	CheckReleaseTarget(context.Context, string, string, string, string) error
	RecordReleaseResult(context.Context, string, string, string, string, ReleaseResult) error
}

func ValidReleaseInput(releaseID, image string) bool {
	return ValidID(releaseID) && len(image) <= api.CellImageReleaseImageMaxBytes && api.ValidDeploymentImage(image)
}

func (r Release) Status() string {
	if r.Generation != r.DesiredGeneration {
		return ReleaseSuperseded
	}
	status := ReleaseAccepted
	for _, target := range r.Targets {
		if target.Outcome == ReleaseBlocked {
			return ReleaseBlocked
		}
		if target.Outcome == ReleaseExpired {
			status = ReleaseExpired
		}
		if target.Outcome == ReleasePending && status != ReleaseExpired {
			status = ReleasePending
		}
	}
	if len(r.Targets) == 0 {
		return ReleasePending
	}
	return status
}

func (result ReleaseResult) valid() bool {
	switch result.Outcome {
	case ReleaseAccepted:
		return ValidID(result.DeploymentID) && result.Diagnostic == ""
	case ReleaseExpired:
		return result.DeploymentID == "" && result.Diagnostic == ""
	case ReleaseBlocked:
		return result.DeploymentID == "" && result.Diagnostic == DiagnosticIdentity
	case ReleasePending:
		return result.DeploymentID == "" && (result.Diagnostic == DiagnosticUnavailable || result.Diagnostic == DiagnosticRejected || result.Diagnostic == DiagnosticReceipt)
	default:
		return false
	}
}

func snapshotMatches(snapshot Target, current Target) bool {
	return current.Resolvable() && snapshot.Binding == current.Binding && snapshot.APIBase == current.APIBase
}
