package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
)

type executionInputFileFlags []string

func (f *executionInputFileFlags) String() string { return strings.Join(*f, ",") }
func (f *executionInputFileFlags) Set(value string) error {
	if len(*f) >= api.ExecutionFileMaxInputs {
		return fmt.Errorf("at most %d input files are allowed", api.ExecutionFileMaxInputs)
	}
	name, local, ok := strings.Cut(value, "=")
	if !ok || local == "" || !runfile.ValidPath(name, api.ExecutionBundleMaxPathSize) {
		return fmt.Errorf("expected RELATIVE_INPUT_PATH=LOCAL_FILE")
	}
	*f = append(*f, value)
	return nil
}

type executionInputFileIDFlags []api.ExecutionFileInput

func (f *executionInputFileIDFlags) String() string { return "" }
func (f *executionInputFileIDFlags) Set(value string) error {
	path, id, ok := strings.Cut(value, "=")
	if !ok || !runfile.ValidPath(path, api.ExecutionBundleMaxPathSize) || !runfile.ValidID(id) || len(*f) >= api.ExecutionFileMaxInputs {
		return fmt.Errorf("expected RELATIVE_INPUT_PATH=FILE_UUID; at most eight inputs")
	}
	*f = append(*f, api.ExecutionFileInput{FileID: id, Path: path})
	return nil
}

type executionLocalInput struct {
	file *os.File
	meta api.ExecutionFileResponse
	path string
}

func prepareExecutionLocalInputs(ctx context.Context, client *api.Client, flags []string) ([]executionLocalInput, error) {
	inputs := []executionLocalInput{}
	fail := func(err error) ([]executionLocalInput, error) { closeExecutionLocalInputs(inputs); return nil, err }
	for _, value := range flags {
		name, local, _ := strings.Cut(value, "=")
		f, err := openCustomerFile(local)
		if err != nil {
			return fail(err)
		}
		inputs = append(inputs, executionLocalInput{file: f, path: name})
		info, err := f.Stat()
		if err != nil {
			return fail(err)
		}
		if !info.Mode().IsRegular() || info.Size() > api.ExecutionFileHardMaxBytes {
			return fail(fmt.Errorf("input file must be regular and at most %d bytes", api.ExecutionFileHardMaxBytes))
		}
		h := sha256.New()
		n, err := io.CopyBuffer(h, io.LimitReader(f, api.ExecutionFileHardMaxBytes+1), make([]byte, api.ExecutionFileStreamChunkBytes))
		if err != nil {
			return fail(err)
		}
		if n != info.Size() {
			return fail(errors.New("input file changed while hashing"))
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return fail(err)
		}
		meta, err := client.PrepareExecutionFile(ctx, api.CreateExecutionFileRequest{SizeBytes: n, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil))})
		if err != nil {
			return fail(err)
		}
		inputs[len(inputs)-1].meta = meta
	}
	return inputs, nil
}
func closeExecutionLocalInputs(inputs []executionLocalInput) {
	for _, f := range inputs {
		_ = f.file.Close()
	}
}
func submitExecutionWithLocalFiles(ctx context.Context, client *api.Client, req api.CreateExecutionRequest, inputs []executionLocalInput) (api.ExecutionResponse, error) {
	if len(inputs) == 0 {
		return client.CreateExecution(ctx, req)
	}
	if api.IdempotencyKeyFromContext(ctx) == "" {
		ctx = api.ContextWithIdempotencyKey(ctx, uuid.NewString())
	}
	for _, f := range inputs {
		req.InputFiles = append(req.InputFiles, api.ExecutionFileInput{FileID: f.meta.ID, Path: f.path})
	}
	receipt, err := client.CreateExecution(ctx, req)
	p := api.AsProblem(err)
	if p == nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			p = &apiErr.Problem
		}
	}
	if p == nil || p.Status != http.StatusConflict || p.Code != "execution_files_not_ready" {
		return receipt, err
	}
	for _, f := range inputs {
		if !f.meta.UploadRequired {
			continue
		}
		if _, err = client.UploadExecutionFile(ctx, f.meta.ID, f.file, f.meta.SizeBytes, f.meta.SHA256); err != nil {
			return api.ExecutionResponse{}, err
		}
	}
	return client.CreateExecution(ctx, req)
}
