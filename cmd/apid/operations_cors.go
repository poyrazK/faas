package main

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// These routes require explicit tenant-bound bearer credentials; account
// cookies cannot authorize them. Browser access therefore uses wildcard CORS
// without credentialed cookies. No account or workload route is included.
func operationCustomerCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := operationCustomerMethod(r.URL.Path)
		if method == "" || r.Header.Get("Origin") == "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "Retry-After, X-Gregale-Artifact-Sha256, Content-Disposition")
		if r.Method == http.MethodOptions {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			requested := r.Header.Get("Access-Control-Request-Method")
			allowed := requested == method
			if method == "GET, POST" {
				allowed = requested == http.MethodGet || requested == http.MethodPost
			}
			if !allowed {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", method)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Last-Event-ID")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func operationCustomerMethod(path string) string {
	const root = "/v1/platform-tenant-self/customer-operations"
	if path == root {
		return "GET, POST"
	}
	if !strings.HasPrefix(path, root+"/") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(path, root+"/"), "/")
	if uuid.Validate(parts[0]) != nil {
		return ""
	}
	if len(parts) == 1 {
		return http.MethodGet
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "events":
			return http.MethodGet
		case "cancel":
			return http.MethodPost
		}
	}
	if len(parts) == 3 && parts[1] == "artifacts" && uuid.Validate(parts[2]) == nil {
		return http.MethodGet
	}
	return ""
}
