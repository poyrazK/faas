package vmmdgrpc

import (
	"context"
	"errors"
	"io"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/runfilestore"
	"github.com/onebox-faas/faas/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) WithExecutionFileStorage(backend storage.StorageBackend) *Server {
	s.executionFileStorage = backend
	return s
}

type executionFileIdentity interface {
	ExecutionFileIdentity(string) (string, string, error)
}

func (s *Server) configureExecutionFiles(instance string, r *executionproto.Request) error {
	if !r.UsesFileTransport() {
		return nil
	}
	identity, ok := s.vmm.(executionFileIdentity)
	if !ok || s.executionFileStorage == nil || r.Manifest == nil {
		return status.Error(codes.Unimplemented, "Run file transport is unavailable")
	}
	account, id, err := identity.ExecutionFileIdentity(instance)
	if err != nil || account != r.Manifest.AccountID || id != r.ExecutionID {
		return status.Error(codes.PermissionDenied, "Run file execution identity is invalid")
	}
	for _, refs := range [][]runfile.Ref{r.Manifest.Inputs, r.Manifest.Outputs} {
		for _, ref := range refs {
			if runfile.ValidateKey(account, ref.ID, ref.StorageKey) != nil {
				return status.Error(codes.InvalidArgument, "invalid Run file scope")
			}
			if err := runfilestore.ValidateRef(account, ref); err != nil {
				return status.Error(codes.InvalidArgument, "invalid Run file key")
			}
		}
	}
	r.HostFiles = &executionproto.HostFileIO{Open: func(ctx context.Context, index int) (io.ReadCloser, error) {
		if index < 0 || index >= len(r.Manifest.Inputs) {
			return nil, errors.New("invalid Run file input")
		}
		return runfilestore.Open(ctx, s.executionFileStorage, account, r.Manifest.Inputs[index])
	}, Put: func(ctx context.Context, index int, reader io.ReadCloser, size int64) (runfile.Descriptor, error) {
		if index < 0 || index >= len(r.Manifest.Outputs) {
			_ = reader.Close()
			return runfile.Descriptor{}, errors.New("invalid Run file output")
		}
		ref := r.Manifest.Outputs[index]
		written, err := runfilestore.Put(ctx, s.executionFileStorage, account, ref, reader, size, "")
		if err != nil {
			return runfile.Descriptor{}, errors.New("run file output publication failed")
		}
		file := ref.Descriptor
		file.SizeBytes = written.SizeBytes
		file.SHA256 = written.SHA256
		return file, nil
	}}
	return nil
}
func (s *Server) ExecuteExecutionFiles(req *vmmdpb.ExecuteExecutionRequest, stream vmmdpb.Vmmd_ExecuteExecutionFilesServer) error {
	if req.GetVersion() < uint32(executionproto.FileVersion) || req.GetVersion() > uint32(executionproto.ClassVersion) {
		return status.Error(codes.InvalidArgument, "Run file protocol v4 required")
	}
	return s.executeExecutionStream(req, stream, true)
}
