package agents

import "context"

// SweepCallbackResultsForTest runs one sweep of expired stored results.
func SweepCallbackResultsForTest(h *Handler, ctx context.Context) { h.sweepCallbackResults(ctx) }

// ResultStubForTest builds the inbox stub of a stored result.
func ResultStubForTest(data []byte, url string, size int, sha string) ([]byte, error) {
	return resultStub(data, callbackResultRef{URL: url, Bytes: size, SHA256: sha})
}
