package api

import "strings"

// EdgeWAFInlineBodyInspectable reports whether a warn or block WAF rule
// scores a request body with this Content-Encoding in-path (ADR-831
// amendment 22); a body it does not score is still sampled off-path. A
// compressed body (any encoding but identity) carries no text the CRS rules
// can read, in-path or sampled.
//
// Every Content-Type is scored. An app may parse a body whatever type the
// client declares, so skipping by type would let a client hide a JSON payload
// behind application/octet-stream; and CRS parses a body of an unknown type
// as one URL-encoded argument (rule 901340), which is cheap. Which bodies are
// read at all (not streams the client expects a reply to before it ends) is
// the gateway's call, as it follows upload admission.
func EdgeWAFInlineBodyInspectable(contentEncoding string) bool {
	enc := strings.ToLower(strings.TrimSpace(contentEncoding))
	return enc == "" || enc == "identity"
}
