package faas

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

var ErrEventConsumerDeliveryContextUnavailable = errors.New("event delivery attempt metadata unavailable; upgrade scheduler and gateway")
var ErrInvalidEventConsumerDeliveryContext = errors.New("invalid event delivery attempt metadata")

// Delivery context is diagnostic, not an authorization assertion or deduplication key.
type EventConsumerDeliveryContext struct {
	Identity                 EventConsumerIdentity
	InvocationID             string
	Source                   string
	Attempt                  int
	ReplayGeneration         int64
	ReplayedFromInvocationID string
	ReplayRootInvocationID   string
}

// EventConsumerDeliveryContextFromHTTP requires authenticated delivery and owner scope.
// Original business receipt identity/body stay independent of attempt metadata.
func EventConsumerDeliveryContextFromHTTP(r *http.Request, body []byte, accountID, appID string) (EventConsumerDeliveryContext, error) {
	var out EventConsumerDeliveryContext
	request, err := EventConsumerRequestFromHTTP(r, body, accountID, appID)
	if err != nil {
		return out, err
	}
	names := []string{"X-Faas-Invocation-Attempt", "X-Faas-Invocation-Replay-Generation", "X-Faas-Invocation-Replayed-From", "X-Faas-Invocation-Replay-Root", "X-Faas-Invocation-Id", "X-Faas-Invocation-Source"}
	values := make([]string, len(names))
	present := make([]bool, len(names))
	for i, name := range names {
		var list []string
		matches := 0
		for key, entries := range r.Header {
			if strings.EqualFold(key, name) {
				present[i] = true
				matches++
				list = append(list, entries...)
			}
		}
		if present[i] && (matches != 1 || len(list) != 1 || list[0] == "") {
			return out, ErrInvalidEventConsumerDeliveryContext
		}
		if present[i] {
			values[i] = list[0]
		}
	}
	if !present[0] && !present[1] && !present[2] && !present[3] {
		return out, ErrEventConsumerDeliveryContextUnavailable
	}
	if !present[0] || !present[1] || !present[4] || !present[5] || !operationUUID(strings.ToLower(values[4])) || (values[5] != "async_invoke" && values[5] != "replay") {
		return out, ErrInvalidEventConsumerDeliveryContext
	}
	attempt, err := strconv.ParseInt(values[0], 10, 32)
	if err != nil || attempt < 1 || strconv.FormatInt(attempt, 10) != values[0] {
		return out, ErrInvalidEventConsumerDeliveryContext
	}
	generation, err := strconv.ParseInt(values[1], 10, 64)
	if err != nil || generation < 0 || strconv.FormatInt(generation, 10) != values[1] {
		return out, ErrInvalidEventConsumerDeliveryContext
	}
	for _, value := range values[2:4] {
		if value != "" && !operationUUID(strings.ToLower(value)) {
			return out, ErrInvalidEventConsumerDeliveryContext
		}
	}
	id, parent, root := strings.ToLower(values[4]), strings.ToLower(values[2]), strings.ToLower(values[3])
	if parent == id || (parent != "" && root == id) {
		return out, ErrInvalidEventConsumerDeliveryContext
	}
	return EventConsumerDeliveryContext{Identity: request.identity, InvocationID: id, Source: values[5], Attempt: int(attempt), ReplayGeneration: generation, ReplayedFromInvocationID: parent, ReplayRootInvocationID: root}, nil
}
