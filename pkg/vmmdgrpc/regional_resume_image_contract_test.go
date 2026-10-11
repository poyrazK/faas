package vmmdgrpc

// adr: 791
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/wire"
)

type combinedRegionalResumeVMM struct {
	*imageReadinessResumeVMM
	placements []runtimequota.Placement
	err        error
}

func (v *combinedRegionalResumeVMM) ResumeRegionalRuntime(_ context.Context, instance string, p runtimequota.Placement) error {
	if instance != p.Permit.InstanceID {
		return runtimequota.ErrBinding
	}
	v.placements = append(v.placements, p.Clone())
	return v.err
}

func TestRegionalWarmHandlerAcknowledgesImageAndSeatTogether(t *testing.T) {
	p := regionalMigrationPlacement(t)
	p.Permit.Kind = runtimequota.CapacityWarm
	p.Seat.Permit = p.Permit
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{"checked", "missing-stored-contract", "native-failure", "malformed-seat"} {
		t.Run(outcome, func(t *testing.T) {
			v := &combinedRegionalResumeVMM{imageReadinessResumeVMM: &imageReadinessResumeVMM{migrationHandlerVMM: &migrationHandlerVMM{}, required: outcome != "missing-stored-contract"}}
			if outcome == "native-failure" {
				v.err = errors.New("fresh readiness failed")
			}
			s := New(v, wire.NewOpsMetrics("regional_checked_resume_test"), "1.10.0", nil)
			req := &vmmdpb.ResumeWarmInstanceRequest{Instance: p.Permit.InstanceID, RegionalRuntime: raw, ImageHealthcheckRequired: true}
			if outcome == "malformed-seat" {
				req.RegionalRuntime = []byte("null")
			}
			response, err := s.ResumeWarmInstance(t.Context(), req)
			if outcome == "checked" {
				if err != nil || !response.GetImageHealthcheckVerified() || !response.GetSupportsImageHealthcheckMonitoring() || !bytes.Equal(response.GetRegionalRuntime(), raw) || response.GetInstance() != req.Instance {
					t.Fatal("combined acknowledgement lost a required contract", response, err)
				}
			} else if err == nil || response != nil {
				t.Fatal("refused combined resume acknowledged serving", response, err)
			}
			want := 0
			if outcome == "checked" || outcome == "native-failure" {
				want = 1
			}
			if len(v.placements) != want || v.resumes != 0 {
				t.Fatal("combined handler used independent fallback", len(v.placements), v.resumes)
			}
		})
	}
}

func TestRegionalResumePreservesImageHealthcheckWireTags(t *testing.T) {
	req := (&vmmdpb.ResumeWarmInstanceRequest{}).ProtoReflect().Descriptor().Fields()
	resp := (&vmmdpb.ResumeWarmInstanceResponse{}).ProtoReflect().Descriptor().Fields()
	if req.ByName("image_healthcheck_required").Number() != 2 || req.ByName("regional_runtime").Number() != 3 || resp.ByName("image_healthcheck_verified").Number() != 2 || resp.ByName("supports_image_healthcheck_monitoring").Number() != 3 || resp.ByName("regional_runtime").Number() != 4 {
		t.Fatal("integration reinterpreted shipped image-readiness wire fields")
	}
}
