package main

import (
	"net/http"
	"strings"
)

func handleCORSPreflight(config mcpConfig, w http.ResponseWriter, r *http.Request, hasOrigin bool) {
	if r.URL.Path != config.Endpoint {
		http.NotFound(w, r)
		return
	}
	if !hasOrigin {
		w.Header().Set("Allow", "POST, OPTIONS")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
	methods := r.Header.Values("Access-Control-Request-Method")
	if len(methods) != 1 || (methods[0] != "GET" && methods[0] != "POST") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "method_not_allowed"})
		return
	}
	headers := []string{}
	for _, value := range r.Header.Values("Access-Control-Request-Headers") {
		for _, raw := range strings.Split(value, ",") {
			header := strings.ToLower(strings.TrimSpace(raw))
			if header == "" {
				continue
			}
			allowed := false
			switch header {
			case "authorization", "content-type", "accept", "mcp-protocol-version", "mcp-method", "mcp-name":
				allowed = true
			default:
				if strings.HasPrefix(header, "mcp-param-") && len(header) > len("mcp-param-") {
					allowed = true
					for _, ch := range strings.TrimPrefix(header, "mcp-param-") {
						if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || strings.ContainsRune("!#$%&'*+.^_`|~-", ch)) {
							allowed = false
							break
						}
					}
				}
			}
			if !allowed {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "header_not_allowed"})
				return
			}
			headers = append(headers, header)
		}
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", strings.Join(headers, ", "))
	w.WriteHeader(http.StatusNoContent)
}
