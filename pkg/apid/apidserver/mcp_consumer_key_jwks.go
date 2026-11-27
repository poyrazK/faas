package apidserver

import "net/http"

// serveMCPConsumerKeyJWKS answers the empty JSON Web Key Set named by api-key
// MCP policies (ADR-20261122194324). No token can verify against it.
func serveMCPConsumerKeyJWKS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/jwk-set+json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(`{"keys":[]}`))
}
