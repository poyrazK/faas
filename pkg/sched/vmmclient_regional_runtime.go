package sched

// adr: 493
import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
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
