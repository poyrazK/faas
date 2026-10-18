package appevents

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// eventDecodeProblem maps a publish or preview body decode error. A request
// over the JSON body cap is a 413 naming the cap; it used to answer "invalid
// JSON body" for a valid but large event (H10-69). Batch publish has its own
// event_publish_batch_too_large envelope.
func eventDecodeProblem(r *http.Request, err error) *api.Problem {
	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		return api.ErrValidation("invalid JSON body")
	}
	observed := r.ContentLength
	if observed <= tooLarge.Limit {
		// A chunked body has no declared length; report "over the cap".
		observed = tooLarge.Limit + 1
	}
	return api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeRequestTooLarge, "Event too large",
		fmt.Sprintf("the event request is above the %d-byte cap; send large payloads by reference (for example an object key)", tooLarge.Limit)).
		WithByteLimit(tooLarge.Limit, observed).
		WithDocs("https://gregale.dev/docs/event-driven")
}
