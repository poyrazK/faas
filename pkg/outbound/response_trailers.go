package outbound

import (
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/net/http/httpguts"
)

func providerResponseTrailers(resp *http.Response) http.Header {
	if len(resp.Trailer) == 0 {
		return nil
	}
	trailers := make(http.Header)
	for name, values := range resp.Trailer {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidTrailerHeader(name) || isHopByHop(resp.Header, name) {
			continue
		}
		trailers[name] = append([]string(nil), values...)
	}
	return trailers
}

func declareProviderTrailers(w http.ResponseWriter, resp *http.Response) {
	trailers := providerResponseTrailers(resp)
	if len(trailers) == 0 {
		return
	}
	// HTTP/2 can carry Content-Length alongside trailers; downstream
	// HTTP/1.1 needs chunked framing to preserve the same metadata.
	w.Header().Del("Content-Length")
	for name := range trailers {
		w.Header().Add("Trailer", name)
	}
}

func finishProviderTrailers(w http.ResponseWriter, resp *http.Response, maxBytes int64, maxCount int) error {
	if !responseHeadersWithinBounds(resp.Header, maxBytes, maxCount, resp.Trailer) {
		return fmt.Errorf("provider response trailers exceed the gateway limit")
	}
	trailers := providerResponseTrailers(resp)
	if len(trailers) == 0 {
		return nil
	}
	// Flush prevents a small response from gaining an automatic
	// Content-Length before undeclared trailers are attached.
	if err := http.NewResponseController(w).Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return fmt.Errorf("flush provider response trailers: %w", err)
	}
	for name, values := range trailers {
		w.Header()[http.TrailerPrefix+name] = values
	}
	return nil
}
