// adr: 20261018183000
package api

import "encoding/json"

// A running Operation starts child Operations for its customer. The key is
// the parent's name for the child: the same key always means the same child.
const (
	OperationChildKeyMaxBytes  = 64
	OperationChildBodyMaxBytes = OperationSubmissionMaxBytes + 1024
	// OperationChildrenSettledEvent is the event the platform signals to a
	// parent waiting on it once every child it started has settled. For
	// accounts with child Operations it is reserved for the platform.
	OperationChildrenSettledEvent = "operation.children.settled"
)

// OperationChildrenSettled is the payload of the children-settled signal.
type OperationChildrenSettled struct {
	Total     int64 `json:"total"`
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	Cancelled int64 `json:"cancelled"`
}

// OperationChildRequest starts one child. Operation names a definition of
// the parent's deployment.
type OperationChildRequest struct {
	Operation string          `json:"operation"`
	Key       string          `json:"key"`
	Input     json.RawMessage `json:"input"`
}

// OperationChild is one child with the parent's key for it.
type OperationChild struct {
	Key       string            `json:"key"`
	Operation OperationResponse `json:"operation"`
}

// OperationChildStartResponse answers a child start. Replayed is true when
// the key already named a child, which is returned unchanged.
type OperationChildStartResponse struct {
	OperationChild
	Replayed bool `json:"replayed"`
}

type OperationChildrenResponse struct {
	Children []OperationChild `json:"children"`
}
