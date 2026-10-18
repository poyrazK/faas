package gateway

import "net/http"

// A 101 ends HTTP response negotiation; the other 1xx statuses precede a
// separate final response and must not claim its status or metadata.
func isInformationalStatus(status int) bool {
	return status >= 100 && status < 200 && status != http.StatusSwitchingProtocols
}

// writeInformationalResponse forwards one header block without leaving it in
// the downstream writer's final-response headers. This matters when the outer
// writer buffers its own header map, or strips private metadata from the
// interim response while retaining it for final-response accounting.
func writeInformationalResponse(dst http.ResponseWriter, status int, header http.Header) {
	live := dst.Header()
	previous, next := live.Clone(), header.Clone()
	defer func() {
		clear(live)
		for key, values := range previous {
			live[key] = values
		}
	}()
	clear(live)
	for key, values := range next {
		live[key] = values
	}
	dst.WriteHeader(status)
}
