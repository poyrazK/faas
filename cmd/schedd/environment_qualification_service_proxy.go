package main

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	environmentQualificationServiceProxyHTTPEnv  = "FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_SERVICE_PROXY_HTTP_URL"
	environmentQualificationServiceProxyHTTPSEnv = "FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_SERVICE_PROXY_HTTPS_URL"
)

// environmentQualificationServiceProxyResolver exposes only the local node's
// private guest-service listener to qualification. A fleet-wide central
// schedd cannot route to arbitrary tenant bridges, so a different selected
// node or an unset transport URL fails closed before candidate admission.
func environmentQualificationServiceProxyResolver(store environmentQualificationRecoveryNodeStore,
	ownerNodeID, httpURL, httpsURL string,
) func(context.Context, string, api.ServiceBindingTransport) (string, error) {
	httpURL, httpsURL = strings.TrimSpace(httpURL), strings.TrimSpace(httpsURL)
	return func(ctx context.Context, nodeID string, transport api.ServiceBindingTransport) (string, error) {
		if nodeID == "" || store == nil {
			return "", state.ErrEnvironmentWorkloadPreparationUnavailable
		}
		localNodeID, err := environmentQualificationRecoveryNodeID(ctx, store, ownerNodeID)
		if err != nil {
			return "", err
		}
		if localNodeID == "" || localNodeID != nodeID {
			return "", state.ErrConflict
		}
		switch transport.Effective() {
		case api.ServiceBindingTransportHTTP:
			if httpURL == "" {
				return "", state.ErrEnvironmentWorkloadPreparationUnavailable
			}
			return httpURL, nil
		case api.ServiceBindingTransportHTTPS:
			if httpsURL == "" {
				return "", state.ErrEnvironmentWorkloadPreparationUnavailable
			}
			return httpsURL, nil
		default:
			return "", state.ErrEnvironmentWorkloadPreparationUnavailable
		}
	}
}
