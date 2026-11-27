package api

import "testing"

func TestEdgeWAFInlineBodyInspectable(t *testing.T) {
	for _, tc := range []struct {
		name          string
		contentLength int64
		contentType   string
		encoding      string
		want          bool
	}{
		{name: "JSON with length", contentLength: 40, contentType: "application/json", want: true},
		{name: "form with length", contentLength: 12, contentType: "application/x-www-form-urlencoded", want: true},
		{name: "chunked JSON", contentLength: -1, contentType: "application/json; charset=utf-8", want: true},
		// A client may hide a payload behind any declared type.
		{name: "octet-stream with length", contentLength: 100, contentType: "application/octet-stream", want: true},
		{name: "chunked without type", contentLength: -1, want: true},
		{name: "chunked unparsable type", contentLength: -1, contentType: "{{", want: true},
		{name: "identity encoding", contentLength: 40, contentType: "application/json", encoding: "Identity", want: true},
		{name: "gzip", contentLength: 40, contentType: "application/json", encoding: "gzip", want: false},
		{name: "br on chunked", contentLength: -1, contentType: "application/json", encoding: "br", want: false},
		{name: "chunked gRPC", contentLength: -1, contentType: "application/grpc+proto", want: false},
		{name: "chunked Connect stream", contentLength: -1, contentType: "application/connect+json", want: false},
		{name: "chunked NDJSON", contentLength: -1, contentType: "application/x-ndjson", want: false},
		{name: "chunked event stream", contentLength: -1, contentType: "Text/Event-Stream", want: false},
		// With a length the client has committed to sending it all.
		{name: "gRPC with length", contentLength: 20, contentType: "application/grpc", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EdgeWAFInlineBodyInspectable(tc.contentLength, tc.contentType, tc.encoding); got != tc.want {
				t.Errorf("EdgeWAFInlineBodyInspectable(%d, %q, %q) = %v, want %v", tc.contentLength, tc.contentType, tc.encoding, got, tc.want)
			}
		})
	}
}
