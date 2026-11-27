package api

import (
	"mime"
	"strings"
)

// EdgeWAFInlineBodyInspectable reports whether a warn or block WAF rule
// scores this request body in-path (ADR-831 amendment 22); a body it does not
// score is still sampled off-path as before.
//
//   - A compressed body (any Content-Encoding but identity) carries no text
//     the CRS rules can read, in-path or sampled.
//   - A body of unknown length (chunked, or HTTP/2 without Content-Length)
//     declared as a streaming type is not read: a gRPC or NDJSON client may
//     send one message and wait for the reply, and the gateway would wait
//     for bytes that never come.
//
// Every other Content-Type is scored. An app may parse a body whatever type
// the client declares, so skipping by type would let a client hide a JSON
// payload behind application/octet-stream; and CRS parses a body of an
// unknown type as one URL-encoded argument (rule 901340), which is cheap.
func EdgeWAFInlineBodyInspectable(contentLength int64, contentType, contentEncoding string) bool {
	if enc := strings.ToLower(strings.TrimSpace(contentEncoding)); enc != "" && enc != "identity" {
		return false
	}
	if contentLength >= 0 {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return true
	}
	return !edgeWAFStreamingMediaType(mediaType)
}

func edgeWAFStreamingMediaType(mediaType string) bool {
	if strings.HasPrefix(mediaType, "application/grpc") || strings.HasPrefix(mediaType, "application/connect+") {
		return true
	}
	switch mediaType {
	case "application/x-ndjson", "application/ndjson", "application/jsonl", "application/x-jsonlines",
		"application/json-seq", "application/stream+json", "text/event-stream":
		return true
	}
	return false
}
