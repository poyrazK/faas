package main

import (
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
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

// usageExportJobsForRole keeps provider usage exports, and the provider
// account keys they need, on the control plane: a compute node's gateway
// runs none (ADR-20261031122757 §4).
func usageExportJobsForRole(r role.Role, registry *objectstorage.Registry, store state.ObjectStorageProviderUsageStore, getenv func(string) string) ([]objectstorage.UsageExportJob, error) {
	if r == role.RoleComputeOnly {
		return nil, nil
	}
	return usageExportJobs(registry, store, getenv)
}
