package sched

// adr: 595 Missing or invalid cache evidence retains verified cold fallback.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/protobuf/proto"
)

type refusedRestoreCatalogStore struct {
	state.Store
	state.ApplicationStandardSnapshotCaptureStore
	record state.ApplicationStandardSnapshotCaptureRecord
	err    error
}

func (s refusedRestoreCatalogStore) GetApplicationStandardSnapshotCapture(context.Context, string, string, string, string) (state.ApplicationStandardSnapshotCaptureRecord, error) {
	return s.record, s.err
}

func TestStandardRestoreCatalogRefusalKeepsColdAuthority(t *testing.T) {
	readFailure := errors.New("catalog read failed")
	for _, tc := range []struct {
		name    string
		record  state.ApplicationStandardSnapshotCaptureRecord
		err     error
		wantErr bool
	}{
		{name: "collected", err: state.ErrNotFound},
		{name: "incomplete grant"},
		{name: "incomplete acknowledgment", record: state.ApplicationStandardSnapshotCaptureRecord{Acknowledgment: &runtimeadmission.SnapshotAcknowledgment{}}},
		{name: "read failure", err: readFailure, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &Engine{store: refusedRestoreCatalogStore{Store: state.NewMemStore(), record: tc.record, err: tc.err}}
			binding := runtimeadmission.Binding{AccountID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString()}
			req := &vmmdpb.CreateAdmittedRuntimeRequest{Binding: binding.ToProto()}
			before := proto.Clone(req)
			out, err := e.prepareStandardSnapshotRestore(t.Context(), runtimeadmission.Identity{SnapshotRestoreVersion: runtimeadmission.SnapshotRestoreVersion}, req,
				&SnapshotRef{ApplicationStandardCaptureToken: uuid.NewString()})
			if tc.wantErr {
				if out != nil || !errors.Is(err, readFailure) {
					t.Fatalf("catalog read failure ignored: %+v %v", out, err)
				}
				return
			}
			if err != nil || out != req || !proto.Equal(before, out) || out.SnapshotRestore != nil {
				t.Fatalf("cache refusal changed verified cold authority: %+v %v", out, err)
			}
		})
	}
}
