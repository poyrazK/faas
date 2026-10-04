package sched

// adr: 493
import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/wire"
)

func regionalRuntimeToProto(p *runtimequota.Placement) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	if p.Validate() != nil {
		return nil, runtimequota.ErrInvalid
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if len(raw) > api.RegionalRuntimeHostRecordMaxBytes {
		return nil, runtimequota.ErrInvalid
	}
	return raw, nil
}

func regionalRuntimeForLaunch(instance string, app AppSpec, purpose string) ([]byte, error) {
	if app.RegionalRuntime != nil {
		if err := app.RegionalRuntime.ValidateBinding(runtimequota.LifecycleBinding{InstanceID: instance, AppID: app.AppID, AccountID: app.AccountID, DeploymentID: app.DeploymentID, Plan: app.Plan, Purpose: purpose}); err != nil {
			return nil, err
		}
	}
	return regionalRuntimeToProto(app.RegionalRuntime)
}

func regionalRuntimeCorrelation(fields wire.CorrelationFields, app AppSpec) (wire.CorrelationFields, error) {
	if app.RegionalRuntime != nil {
		if fields.DeploymentID != "" && fields.DeploymentID != app.DeploymentID {
			return fields, runtimequota.ErrBinding
		}
		fields.DeploymentID = app.DeploymentID
	}
	return fields, nil
}
