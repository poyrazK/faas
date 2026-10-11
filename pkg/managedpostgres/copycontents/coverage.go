package copycontents

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// CoverageError names a private unsupported input for the owning worker. Error,
// formatted and JSON output never disclose SQL object names or data. The worker
// maps its reason to the already authorized logical database binding in status.
type CoverageError struct {
	reason string
	object name
}

func (*CoverageError) Error() string      { return "PostgreSQL stored contents coverage unsupported" }
func (e *CoverageError) String() string   { return e.Error() }
func (e *CoverageError) GoString() string { return e.Error() }
func (*CoverageError) Unwrap() error      { return pgerrors.ErrUnsupported }
func (e *CoverageError) Reason() string   { return e.reason }
func (e *CoverageError) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Reason string `json:"reason"`
	}{e.reason})
}

// ObjectForWorker is private metadata; never send it to ordinary logs or a
// cross-account status response. Database resource names come from clone scope.
func (e *CoverageError) ObjectForWorker() (schema, object string) {
	return e.object.Schema, e.object.Name
}
func unsupported(reason string, object name) error { return &CoverageError{reason, object} }
