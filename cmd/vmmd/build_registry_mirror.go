package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/registryproxy"
)

// buildRegistryMirrorFromEnv reads FAAS_VMMD_BUILD_REGISTRY_MIRROR
// (ADR-20261110210000 slice 3j-3). Unset or "off": a builder VM gets a
// Docker config with its app's registry passwords, opened here with the
// build identity. "secretd": the builder pulls those registries through a
// mirror vmmd serves on its vsock, backed by the control plane's
// RegistryFetch by build id, and no password is opened on this node or
// given to the guest. That is what lets the build identity leave compute.
func buildRegistryMirrorFromEnv(ctx context.Context, getenv func(string) string, log *slog.Logger) (fcvm.BuildRegistryMirror, func(), error) {
	switch mode := getenv("FAAS_VMMD_BUILD_REGISTRY_MIRROR"); mode {
	case "", "off":
		return nil, func() {}, nil
	case "secretd":
	default:
		return nil, nil, fmt.Errorf("vmmd: FAAS_VMMD_BUILD_REGISTRY_MIRROR=%q: want off or secretd", mode)
	}
	rpc, closeConn, err := dialSecretd(ctx, getenv, "FAAS_VMMD_BUILD_REGISTRY_MIRROR", log)
	if err != nil {
		return nil, nil, err
	}
	proxy := &registryproxy.Proxy{Client: rpc}
	log.Info("vmmd: builder VMs pull private registries through secretd")
	return func(buildID, registry string) http.Handler {
		return &registryproxy.Mirror{Registry: registry, Transport: proxy.ForBuild(buildID)}
	}, closeConn, nil
}
