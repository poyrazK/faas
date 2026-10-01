package main

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/devbridge"
)

//go:embed dev_bridge_inspection.html
var bridgeInspectionPage []byte

func bridgeInspectionHandler(inspector *devbridge.Inspector) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /requests", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(inspector.Snapshot())
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(bridgeInspectionPage)
	})
	return mux
}
