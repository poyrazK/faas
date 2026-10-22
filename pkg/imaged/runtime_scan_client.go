package imaged

import (
	"context"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimescan"
)

type RuntimeScanMaterializer interface {
	MaterializeRuntimeScan(context.Context, runtimescan.Request) (runtimescan.Receipt, error)
}

var _ RuntimeScanMaterializer = (*VMMClient)(nil)

func (c *VMMClient) MaterializeRuntimeScan(ctx context.Context, request runtimescan.Request) (runtimescan.Receipt, error) {
	request.Sources = slices.Clone(request.Sources)
	if c == nil {
		return runtimescan.Receipt{}, errors.New("native scanner view client unavailable")
	}
	if err := request.Validate(); err != nil {
		return runtimescan.Receipt{}, err
	}
	cli, err := c.dial(ctx)
	if err != nil {
		return runtimescan.Receipt{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, api.ApplicationStandardArtifactScanTimeout)
	defer cancel()
	response, err := cli.MaterializeRuntimeScan(callCtx, request.ToProto())
	if err != nil {
		return runtimescan.Receipt{}, err
	}
	if err := callCtx.Err(); err != nil {
		return runtimescan.Receipt{}, err
	}
	return runtimescan.ReceiptFromProto(response, request)
}
