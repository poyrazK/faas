// adr: 1197
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// An event wait parks an operation workflow until a signal of its event name
// arrives, or its timeout passes.
const (
	OperationWaitEvent              = "event"
	OperationFailureEventExpiry     = "event_timeout"
	OperationSignalIDMaxBytes       = 128
	OperationSignalEventMaxBytes    = 63
	OperationSignalPayloadMaxBytes  = 64 << 10
	OperationSignalBodyMaxBytes     = OperationSignalPayloadMaxBytes + 2048
	OperationSignalSubjectTargetMax = 16
)

// OperationSignalRequest delivers one named event to an Operation. SignalID
// makes delivery idempotent: a repeat with the same event and payload returns
// the original receipt; a different event or payload under it conflicts.
type OperationSignalRequest struct {
	SignalID string          `json:"signal_id"`
	Event    string          `json:"event"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// OperationSubjectSignalRequest signals every active Operation of one name
// about one business subject, so a caller needs no Operation IDs.
type OperationSubjectSignalRequest struct {
	Scope     string           `json:"scope"`
	Operation string           `json:"operation"`
	Subject   OperationSubject `json:"subject"`
	SignalID  string           `json:"signal_id"`
	Event     string           `json:"event"`
	Payload   json.RawMessage  `json:"payload,omitempty"`
}

// OperationSignalReceipt reports one delivery. Duplicate marks a repeat of an
// earlier signal; Waiting reports whether the Operation was waiting for this
// event when it arrived (otherwise the signal is held for a later wait).
type OperationSignalReceipt struct {
	OperationID string `json:"operation_id"`
	SignalID    string `json:"signal_id"`
	Event       string `json:"event"`
	Duplicate   bool   `json:"duplicate"`
	Waiting     bool   `json:"waiting"`
}

type OperationSubjectSignalResponse struct {
	Delivered []OperationSignalReceipt `json:"delivered"`
}

func (c *Client) SignalOperation(ctx context.Context, slug, id string, req OperationSignalRequest) (OperationSignalReceipt, error) {
	var out OperationSignalReceipt
	err := c.do(ctx, http.MethodPost, operationAppPath(slug, id)+"/signals", req, &out)
	return out, err
}

func (c *Client) SignalOperationsBySubject(ctx context.Context, slug string, req OperationSubjectSignalRequest) (OperationSubjectSignalResponse, error) {
	var out OperationSubjectSignalResponse
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/operation-signals", req, &out)
	return out, err
}
