// ADR-853: verdict-only pure application validation; no transition builder.
package faas

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

type DurableEntityRestoreValidationRequest struct {
	ProtocolVersion int                   `json:"protocol_version"`
	Event           string                `json:"event"`
	Entity          DurableEntityIdentity `json:"entity"`
	RequestID       string                `json:"request_id"`
	DeploymentID    string                `json:"deployment_id"`
	ExpectedVersion uint64                `json:"expected_version"`
	SourceVersion   uint64                `json:"source_version"`
	Candidate       json.RawMessage       `json:"candidate"`
}

func DecodeDurableEntityRestoreValidationRequest(body []byte) (DurableEntityRestoreValidationRequest, error) {
	var request DurableEntityRestoreValidationRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var extra any
	if len(body) > DurableEntityHandlerMaxRequestBytes || !utf8.Valid(body) || dec.Decode(&request) != nil || dec.Decode(&extra) != io.EOF || request.ProtocolVersion != DurableEntityRestoreValidationProtocolVersion || request.Event != "validate_restore" || request.ExpectedVersion == 0 || request.SourceVersion == 0 || !json.Valid(request.Candidate) || len(request.Candidate) > DurableEntityHandlerMaxTransitionBytes {
		return request, ErrDurableEntityHandler
	}
	for _, identity := range []string{request.Entity.AccountID, request.Entity.AppID, request.Entity.Namespace, request.Entity.Key, request.RequestID, request.DeploymentID} {
		if !durableEntityIdentity(identity, 0) {
			return request, ErrDurableEntityHandler
		}
	}
	for _, identity := range []string{request.Entity.EnvironmentID, request.Entity.TenantID} {
		if identity != "" && !durableEntityIdentity(identity, 0) {
			return request, ErrDurableEntityHandler
		}
	}
	return request, nil
}

// EncodeDurableEntityRestoreValidation runs a synchronous pure validator. An
// application validation error returns false without exposing its text. Validators
// must not perform I/O. Parsing this envelope is not public-route authentication.
func EncodeDurableEntityRestoreValidation(body []byte, validate func(DurableEntityRestoreValidationRequest) error) ([]byte, error) {
	request, err := DecodeDurableEntityRestoreValidationRequest(body)
	if err != nil || validate == nil {
		return nil, ErrDurableEntityHandler
	}
	return json.Marshal(struct {
		ProtocolVersion int  `json:"protocol_version"`
		Valid           bool `json:"valid"`
	}{DurableEntityRestoreValidationProtocolVersion, validate(request) == nil})
}
