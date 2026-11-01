package main

import (
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

// checkComputeGateway holds a gateway on a compute node to what
// ADR-20261031122757 §4 lets it hold. Compute nodes run tenant code, so the
// gateway there opens S3 credentials with the storage seal domain alone and
// never the fleet or host identity, reaches every backend with its gateway
// credential and never the bucket-admin one, and keeps to its share of the
// node's port (§3).
func checkComputeGateway(getenv func(string) string, storage []*age.X25519Identity, registry *objectstorage.Registry) error {
	if len(storage) == 0 {
		return errors.New("s3-gatewayd: a compute node's gateway needs the storage seal domain")
	}
	if getenv("FAAS_HOST_AGE_IDENTITY_PATH") != "" {
		return errors.New("s3-gatewayd: a compute node's gateway must not hold the fleet or host identity")
	}
	if registry.Transfer.MaxBytesPerSecond == 0 {
		return errors.New("s3-gatewayd: a compute node's gateway needs transfer.max_bytes_per_second, its share of the node's port")
	}
	for _, b := range registry.Backends() {
		if !b.GatewayCredential {
			return fmt.Errorf("s3-gatewayd: backend %s names no gateway credential; a compute node's gateway never holds the bucket-admin one", b.ID)
		}
	}
	return nil
}

// checkListeners keeps the data listener on loopback behind the TLS edge or
// the node's s3 tunnel connector. The control listener (readiness, metrics)
// may bind beyond loopback only on a compute node, where the control plane
// scrapes it and nftables admits only control-plane addresses
// (ADR-20261031122757 §2).
func checkListeners(r role.Role, listenAddr, controlAddr string) error {
	if !s3gateway.IsLoopbackAddress(listenAddr) {
		return errors.New("s3-gatewayd: the data listener must be a loopback address behind the TLS edge")
	}
	if r != role.RoleComputeOnly && !s3gateway.IsLoopbackAddress(controlAddr) {
		return errors.New("s3-gatewayd: the control listener must be a loopback address outside a compute node")
	}
	return nil
}
