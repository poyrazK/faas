package main

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// publicObjectStorageRoot serves public bucket reads and policy-controlled
// uploads on app hostnames in front of next. The edge store implements every
// store the handlers take (edge_store_contract.go), so both are always on.
func publicObjectStorageRoot(store *state.EdgeStore, registry *objectstorage.Registry, enabled func() bool,
	appsDomain string, next http.Handler, log *slog.Logger) (http.Handler, error) {
	publicHandler, err := objectstorage.NewPublicReadHandler(objectstorage.PublicReadConfig{
		Store: store, Registry: registry, RequestMetrics: store, Accounting: store,
		Enabled: enabled, AppsDomain: appsDomain, Next: next, Log: log,
	})
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: public object storage handler: %w", err)
	}
	upload, err := objectstorage.NewUploadHandler(objectstorage.UploadConfig{
		Store: store, Routes: store, Buckets: store, Authenticator: store,
		Registry: registry, Accounting: store, RequestMetrics: store, Enabled: enabled,
		AppsDomain: appsDomain, Next: publicHandler, Log: log,
	})
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: object upload handler: %w", err)
	}
	log.Info("gatewayd-public: policy-controlled object uploads enabled")
	log.Info("gatewayd-public: public object storage reads enabled")
	return upload, nil
}
