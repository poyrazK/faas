// Package httpkit is the request/response kit shared by cmd/apid and the
// pkg/apid/<domain> packages that are being lifted out of it (ADR-982).
//
// Everything here is stateless: no *server, no store, no globals. A domain
// package that needs the authenticated app takes an AppLoader; one that needs
// a logger takes a *slog.Logger. cmd/apid keeps thin facades (writeJSON,
// decodeJSON, s.notFound, …) that delegate here, so the ~2,000 existing call
// sites are untouched while new and moved code imports this package directly.
package httpkit

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// DefaultJSONBodyMaxBytes caps control-plane JSON request bodies when the
// caller does not pass a route-specific limit.
const DefaultJSONBodyMaxBytes int64 = 1 << 20

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON decodes a request body capped at DefaultJSONBodyMaxBytes.
func DecodeJSON(r *http.Request, v any) error {
	return DecodeJSONSized(r, v, DefaultJSONBodyMaxBytes)
}

// DecodeJSONSized is the single bounded JSON decoder for apid control-plane
// requests. It rejects unknown fields and consumes the remainder so both
// Content-Length and chunked requests are held to the same byte cap.
func DecodeJSONSized(r *http.Request, v any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = DefaultJSONBodyMaxBytes
	}
	if r.Body == nil {
		return io.EOF
	}
	if r.ContentLength > maxBytes {
		return &http.MaxBytesError{Limit: maxBytes}
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Consume the remainder so chunked requests with a valid JSON prefix
	// and an oversized/trailing payload cannot evade the reader cap.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single JSON value")
		}
		return err
	}
	return nil
}

// DecodeJSONLimit is a MaxBytesReader-wrapped variant of DecodeJSON.
// The plan's MaxSourceBytesPerInvocation caps each event-driven payload
// (Hobby 64 KB, Pro 256 KB, Scale 1 MB); anything larger is a 413, not
// a 422 — the size limit is a plan cap, not a malformed-body problem.
//
// Returns false if the read or decode fails; the helper writes the
// appropriate Problem response in that case so the caller can simply
// `return`.
func DecodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // 1 MB hard fallback — defensive only
	}
	if r.ContentLength > maxBytes {
		api.WriteProblem(w, api.ErrPlanSourceBytes(int(maxBytes), r.ContentLength))
		return false
	}
	if r.Body == nil {
		api.WriteProblem(w, api.ErrValidation("empty request body"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			api.WriteProblem(w, api.ErrValidation("empty request body"))
			return false
		}
		// MaxBytesReader returns a *http.MaxBytesError on overflow;
		// surface as a 413 with the source-bytes cap code.
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			api.WriteProblem(w, api.ErrPlanSourceBytes(int(maxBytes), int64(mbe.Limit)))
			return false
		}
		api.WriteProblem(w, api.ErrValidation("malformed JSON: "+err.Error()))
		return false
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrPlanSourceBytes(int(maxBytes), int64(maxBytes)))
			return false
		}
		if err == nil {
			err = errors.New("request body must contain a single JSON value")
		}
		api.WriteProblem(w, api.ErrValidation("malformed JSON: "+err.Error()))
		return false
	}
	return true
}
