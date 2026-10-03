package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

type deploymentSmokeResponseKey struct{}

type deploymentSmokeResponse struct{ deploymentID, proof string }

// Only the handler's authorized smoke branch installs this context. Inbound
// and guest headers cannot manufacture candidate-response evidence.
func withDeploymentSmokeResponse(ctx context.Context, deploymentID, token string) context.Context {
	return context.WithValue(ctx, deploymentSmokeResponseKey{}, deploymentSmokeResponse{deploymentID, apihostingreceipt.CandidateResponseProof(deploymentID, token)})
}

// Called only after the bridge or reverse proxy receives upstream headers.
// Selecting a target or generating a gateway error is insufficient proof.
func stampDeploymentSmokeResponse(ctx context.Context, headers http.Header) {
	if evidence, ok := ctx.Value(deploymentSmokeResponseKey{}).(deploymentSmokeResponse); ok && evidence.deploymentID != "" {
		headers.Set(apihostingreceipt.ServedResponseHeader, evidence.proof)
	}
}

func deploymentSmokeResponseID(ctx context.Context) string {
	evidence, _ := ctx.Value(deploymentSmokeResponseKey{}).(deploymentSmokeResponse)
	return evidence.deploymentID
}
