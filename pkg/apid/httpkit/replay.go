package httpkit

import "net/http"

// IdempotentReplaySkipper is implemented by the Idempotency-Key response
// recorder. A marked response is not stored for replay.
type IdempotentReplaySkipper interface {
	SkipIdempotentReplay()
}

// SkipIdempotentReplay marks a response that carries a one-time credential,
// such as an interactive session's attach token (ADR-1037, ADR-1330), so the
// Idempotency-Key layer never stores it: storing it would keep the plaintext
// token and hand it to every retry. A retry with the key runs the operation
// again. Wrappers that expose Unwrap are followed; without the recorder in
// the chain the call does nothing.
func SkipIdempotentReplay(w http.ResponseWriter) {
	for w != nil {
		if skipper, ok := w.(IdempotentReplaySkipper); ok {
			skipper.SkipIdempotentReplay()
			return
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = unwrapper.Unwrap()
	}
}
