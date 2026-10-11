package main

import (
	"context"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

// The adapter shares rendering and integrity checks, while selecting only
// tenant-self routes. It never forwards a caller-selected account or tenant.
type tenantCustomerOperationsClient struct{ client *api.Client }

func (c tenantCustomerOperationsClient) GetOperation(ctx context.Context, _, id string) (api.OperationResponse, error) {
	return c.client.GetPlatformTenantSelfOperation(ctx, id)
}
func (c tenantCustomerOperationsClient) GetAccountOperationEvents(ctx context.Context, _, id string, after int64) (api.OperationEventsResponse, error) {
	return c.client.GetPlatformTenantSelfOperationEvents(ctx, id, after)
}
func (c tenantCustomerOperationsClient) CancelOperation(ctx context.Context, _, id string, req api.OperationCancellationRequest) (api.OperationResponse, error) {
	return c.client.CancelPlatformTenantSelfOperation(ctx, id, req)
}
func (c tenantCustomerOperationsClient) DownloadOperationArtifact(ctx context.Context, _, id, artifact string, dst io.Writer) (int64, error) {
	return c.client.DownloadPlatformTenantSelfOperationArtifact(ctx, id, artifact, dst)
}
func (c tenantCustomerOperationsClient) ListAccountOperations(context.Context, string, api.OperationListOptions) (api.OperationListResponse, error) {
	return api.OperationListResponse{}, fmt.Errorf("account listing is unavailable in tenant-self mode")
}
func (c tenantCustomerOperationsClient) GetOperationExecutions(context.Context, string, string, int, int) (api.OperationExecutionsResponse, error) {
	return api.OperationExecutionsResponse{}, fmt.Errorf("execution inspection requires account credentials")
}
func (c tenantCustomerOperationsClient) RecoverOperation(context.Context, string, string, api.OperationRecoveryRequest) (api.OperationResponse, error) {
	return api.OperationResponse{}, fmt.Errorf("reconciliation requires account credentials")
}
func (c tenantCustomerOperationsClient) RetryOperationDelivery(context.Context, string, string) (api.OperationResponse, error) {
	return api.OperationResponse{}, fmt.Errorf("delivery retry requires account credentials")
}
