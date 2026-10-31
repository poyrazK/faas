package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// publicObjectStorageRoot serves public bucket reads and policy-controlled
// uploads on app hostnames in front of next. The edge store implements every
// store the handlers take (edge_store_contract.go), so both are always on.
//
// Upload keys are resolved by apid (credentials, ADR 20261029202403): their
// stored hashes are keyed by a secret only apid holds.
func publicObjectStorageRoot(store *state.EdgeStore, credentials objectstorage.UploadAuthenticator, registry *objectstorage.Registry, reads *objectstorage.ReadAdmitter,
	enabled func() bool, domains hostidentity.Domains, next http.Handler, log *slog.Logger) (http.Handler, error) {
	publicHandler, err := objectstorage.NewPublicReadHandler(objectstorage.PublicReadConfig{
		Store: store, Registry: registry, RequestMetrics: store, Accounting: store, ReadAdmitter: reads,
		Enabled: enabled, AppsDomain: domains.Apps, Next: next, Log: log,
	})
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: public object storage handler: %w", err)
	}
	upload, err := objectstorage.NewUploadHandler(objectstorage.UploadConfig{
		Store: store, Routes: store, Buckets: store, Authenticator: credentials,
		Registry: registry, Accounting: store, RequestMetrics: store, Enabled: enabled,
		AppsDomain: domains.Apps, Next: publicHandler, Log: log,
	})
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: object upload handler: %w", err)
	}
	log.Info("gatewayd-public: policy-controlled object uploads enabled")
	log.Info("gatewayd-public: public object storage reads enabled")
	return upload, nil
}

// startPublicReadAdmitter admits public bucket reads from cached decisions
// with batched counts under gateway-safety accounting, against the anonymous
// share of the account's ceilings (ADR-20261030075301 step 2). Its flush loop
// outlives ctx's cancellation; stop ends it after a last flush, so call stop
// after the servers stop and before the pool closes. Other accounting modes
// keep the locking admission.
func startPublicReadAdmitter(ctx context.Context, store *state.EdgeStore, registry *objectstorage.Registry, metrics prometheus.Registerer, log *slog.Logger) (reads *objectstorage.ReadAdmitter, stop func()) {
	if !registry.Accounting.GatewaySafety() {
		return nil, func() {}
	}
	reads = objectstorage.NewNamedReadAdmitter(store, registry.Accounting.AnonymousReadPolicy(), nil, "gatewayd_public_object")
	metrics.MustRegister(reads.Collectors()...)
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		reads.Run(ctx, func(err error) { log.Warn("gatewayd-public: public read usage flush failed", "err", err) })
	}()
	return reads, func() {
		cancel()
		<-done
	}
}
