// adr: 853
package durableentity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRestoreRejected = errors.New("application rejected durable entity restore state")

// RestoreValidationRequest is distinct from the transition protocol. The guest
// receives no live state, alarm, outbox, claim or storage authority.
type RestoreValidationRequest struct {
	ProtocolVersion int             `json:"protocol_version"`
	Event           string          `json:"event"`
	Entity          ID              `json:"entity"`
	RequestID       string          `json:"request_id"`
	DeploymentID    string          `json:"deployment_id"`
	ExpectedVersion uint64          `json:"expected_version"`
	SourceVersion   uint64          `json:"source_version"`
	Candidate       json.RawMessage `json:"candidate"`
}

func (r RestoreValidationRequest) Validate() error {
	if r.ProtocolVersion != api.DurableEntityRestoreValidationProtocolVersion || r.Event != "validate_restore" || !r.Entity.valid() || !validIdentity(r.RequestID) || !validUUID(r.DeploymentID) || r.ExpectedVersion == 0 || r.SourceVersion == 0 || !json.Valid(r.Candidate) {
		return ErrInvalid
	}
	if len(r.Candidate) > api.MaxDurableEntitySnapshotBytes {
		return ErrLimit
	}
	return nil
}

// DecodeRestoreValidation admits a verdict only. Missing valid is not rejection:
// it is a malformed response, as are transitions or unknown response fields.
func DecodeRestoreValidation(body []byte) (bool, error) {
	var response struct {
		ProtocolVersion int   `json:"protocol_version"`
		Valid           *bool `json:"valid"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var extra any
	if len(body) > api.MaxDurableEntityRestoreValidationBytes || dec.Decode(&response) != nil || dec.Decode(&extra) != io.EOF || response.ProtocolVersion != api.DurableEntityRestoreValidationProtocolVersion || response.Valid == nil {
		return false, ErrInvalid
	}
	return *response.Valid, nil
}
