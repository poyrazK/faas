package executionproto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
)

func fileSHA(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}
func TestStoredFilesRoundTripBeyondJSONBudget(t *testing.T) {
	t.Run("v4", func(t *testing.T) { storedFilesRoundTrip(t, false) })
	t.Run("v5_extended_environment", func(t *testing.T) { storedFilesRoundTrip(t, true) })
}
func storedFilesRoundTrip(t *testing.T, extended bool) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data := bytes.Repeat([]byte("abcdef"), 600000)
	req := testRequest()
	req.Version = FileVersion
	if extended {
		req.Version = ClassVersion
		req.Class = api.ExecutionClassExtended
		req.TimeoutMS = 300000
		req.EnvironmentID = uuid.NewString()
	}
	req.InputFiles = []runfile.Descriptor{{ID: uuid.NewString(), Path: "data.bin", SizeBytes: int64(len(data)), SHA256: fileSHA(data)}}
	req.OutputFiles = []string{"copy.bin"}
	req.StoredOutputs = []runfile.Descriptor{{ID: uuid.NewString(), Path: "copy.bin"}}
	req.MaxArtifactBytes = int64(len(data))
	req.HostFiles = &HostFileIO{Open: func(context.Context, int) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }, Put: func(_ context.Context, _ int, r io.ReadCloser, size int64) (runfile.Descriptor, error) {
		defer r.Close()
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, data) || int64(len(got)) != size {
			return runfile.Descriptor{}, errors.New("incorrect output")
		}
		f := req.StoredOutputs[0]
		f.SizeBytes = size
		f.SHA256 = fileSHA(got)
		return f, nil
	}}
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, guest, func(ctx context.Context, r Request, stdout, stderr *OutputWriter) (Result, error) {
			var staged bytes.Buffer
			if err := r.GuestFiles.Receive(ctx, 0, &staged); err != nil {
				return Result{}, err
			}
			if !bytes.Equal(staged.Bytes(), data) {
				return Result{}, errors.New("input mismatch")
			}
			if _, err := stdout.Write([]byte("processed\n")); err != nil {
				return Result{}, err
			}
			f, err := r.GuestFiles.Send(ctx, 0, bytes.NewReader(staged.Bytes()), int64(staged.Len()))
			if err != nil {
				return Result{}, err
			}
			return Result{Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`), Artifacts: []api.ExecutionArtifact{{FileID: f.ID, Name: f.Path, SizeBytes: int(f.SizeBytes), SHA256: f.SHA256}}}, nil
		})
	}()
	client, _ := NewClient(host)
	result, err := client.Execute(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].FileID != req.StoredOutputs[0].ID || string(result.Stdout) != "processed\n" || api.ExecutionArtifactsOutputBytes(result.Artifacts) > req.MaxOutput {
		t.Fatal(result)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestStoredFileFramesRejectOversizeBeforeBodyAllocation(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		var header [8]byte
		binary.BigEndian.PutUint32(header[:4], FrameOutputChunk)
		binary.BigEndian.PutUint32(header[4:], uint32(api.ExecutionFileStreamChunkBytes+5))
		_, _ = guest.Write(header[:])
	}()
	if _, _, err := readFrame(ctx, host); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatal(err)
	}
}
func TestStoredOutputFailureJoinsPublisher(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := testRequest()
	req.Version = FileVersion
	req.OutputFiles = []string{"x"}
	req.StoredOutputs = []runfile.Descriptor{{ID: uuid.NewString(), Path: "x"}}
	req.MaxArtifactBytes = 100
	finished := make(chan struct{})
	req.HostFiles = &HostFileIO{Put: func(context.Context, int, io.ReadCloser, int64) (runfile.Descriptor, error) {
		defer close(finished)
		return runfile.Descriptor{}, errors.New("synthetic publication failure")
	}}
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, guest, func(ctx context.Context, r Request, _, _ *OutputWriter) (Result, error) {
			_, err := r.GuestFiles.Send(ctx, 0, bytes.NewBufferString("hello"), 5)
			return Result{}, err
		})
	}()
	client, _ := NewClient(host)
	if _, err := client.Execute(ctx, req); err == nil {
		t.Fatal("storage failure succeeded")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("publisher not joined")
	}
	_ = host.Close()
	<-done
}
