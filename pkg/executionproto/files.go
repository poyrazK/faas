package executionproto

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
)

const FileVersion uint16 = 4
const ClassVersion uint16 = 5

// UsesFileTransport distinguishes optional file framing from other v5 capabilities.
func (r Request) UsesFileTransport() bool {
	return r.Version == FileVersion || len(r.InputFiles)+len(r.StoredOutputs) > 0 || r.Manifest != nil
}

const (
	FrameInputChunk  uint32 = 8
	FrameInputEnd    uint32 = 9
	FrameOutputStart uint32 = 10
	FrameOutputChunk uint32 = 11
	FrameOutputEnd   uint32 = 12
)

// FileManifest is private scheduler-to-vmmd data, never guest JSON.
type FileManifest struct {
	AccountID string        `json:"account_id"`
	Inputs    []runfile.Ref `json:"inputs,omitempty"`
	Outputs   []runfile.Ref `json:"outputs,omitempty"`
}
type HostFileIO struct {
	Open func(context.Context, int) (io.ReadCloser, error)
	Put  func(context.Context, int, io.ReadCloser, int64) (runfile.Descriptor, error)
}
type GuestFileIO struct {
	Receive func(context.Context, int, io.Writer) error
	Send    func(context.Context, int, io.Reader, int64) (runfile.Descriptor, error)
}

func (r Request) validateFiles() error {
	if len(r.InputFiles) > api.ExecutionFileMaxInputs || len(r.StoredOutputs) > api.ExecutionArtifactMaxFiles || r.MaxArtifactBytes < 0 || r.MaxArtifactBytes > api.ExecutionFileHardMaxBytes {
		return ErrInvalidRequest
	}
	if len(r.InputFiles)+len(r.StoredOutputs) > 0 && r.Version < FileVersion {
		return ErrInvalidRequest
	}
	if len(r.StoredOutputs) > 0 && (len(r.StoredOutputs) != len(r.OutputFiles) || r.MaxArtifactBytes <= 0) {
		return ErrInvalidRequest
	}
	seen := map[string]bool{}
	var total int64
	for _, f := range r.InputFiles {
		if !runfile.ValidID(f.ID) || !runfile.ValidPath(f.Path, api.ExecutionBundleMaxPathSize) || f.SizeBytes < 0 || f.SizeBytes > api.ExecutionFileHardMaxBytes || !runfile.ValidDigest(f.SHA256) || f.Bundle {
			return ErrInvalidRequest
		}
		for p := range seen {
			if p == f.Path || strings.HasPrefix(p, f.Path+"/") || strings.HasPrefix(f.Path, p+"/") {
				return ErrInvalidRequest
			}
		}
		seen[f.Path] = true
		total += f.SizeBytes
	}
	if total > api.ExecutionFileHardMaxBytes {
		return ErrInvalidRequest
	}
	seen = map[string]bool{}
	for i, f := range r.StoredOutputs {
		if !runfile.ValidID(f.ID) || seen[f.ID] || f.Path != r.OutputFiles[i] || f.SizeBytes != 0 || f.SHA256 != "" || f.Bundle {
			return ErrInvalidRequest
		}
		seen[f.ID] = true
	}
	return nil
}
func descriptors(refs []runfile.Ref) []runfile.Descriptor {
	out := make([]runfile.Descriptor, len(refs))
	for i, r := range refs {
		out[i] = r.Descriptor
	}
	return out
}
func fileChunk(index int, data []byte) []byte {
	body := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(body, uint32(index))
	copy(body[4:], data)
	return body
}
func fileIndex(body []byte, index int) bool {
	return len(body) >= 4 && binary.BigEndian.Uint32(body[:4]) == uint32(index)
}
func (c *Client) sendInputs(ctx context.Context, r Request) error {
	if len(r.InputFiles) == 0 {
		return nil
	}
	if r.HostFiles == nil || r.HostFiles.Open == nil {
		return ErrInvalidRequest
	}
	buf := make([]byte, api.ExecutionFileStreamChunkBytes)
	for i, f := range r.InputFiles {
		body, err := r.HostFiles.Open(ctx, i)
		if err != nil {
			return errors.New("execution file input could not be opened")
		}
		h := sha256.New()
		var n int64
		for {
			count, readErr := body.Read(buf)
			if count > 0 {
				n += int64(count)
				_, _ = h.Write(buf[:count])
				if n > f.SizeBytes {
					err = ErrInvalidRequest
					break
				}
				if err = writeFrame(ctx, c.conn, FrameInputChunk, fileChunk(i, buf[:count])); err != nil {
					break
				}
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					err = readErr
				}
				break
			}
			if count == 0 {
				err = io.ErrNoProgress
				break
			}
		}
		closeErr := body.Close()
		if err != nil || closeErr != nil || n != f.SizeBytes || "sha256:"+hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return errors.New("execution file input transfer failed integrity verification")
		}
		if err = writeFrame(ctx, c.conn, FrameInputEnd, fileChunk(i, nil)); err != nil {
			return err
		}
	}
	return nil
}

type fileStart struct {
	Index int   `json:"index"`
	Size  int64 `json:"size_bytes"`
}
type outputTransfer struct {
	req     Request
	reader  *io.PipeReader
	writer  *io.PipeWriter
	done    chan outputDone
	written []runfile.Descriptor
	size    int64
	bytes   int64
	active  bool
}
type outputDone struct {
	file runfile.Descriptor
	err  error
}

func (t *outputTransfer) close() {
	if t.writer != nil {
		_ = t.writer.CloseWithError(context.Canceled)
	}
	if t.reader != nil {
		_ = t.reader.CloseWithError(context.Canceled)
	}
	if t.done != nil {
		<-t.done
		t.done = nil
	}
}
func (t *outputTransfer) frame(ctx context.Context, kind uint32, body []byte) error {
	index := len(t.written)
	if t.req.Version < FileVersion || index >= len(t.req.StoredOutputs) || t.req.HostFiles == nil || t.req.HostFiles.Put == nil {
		return ErrUnexpectedFrame
	}
	switch kind {
	case FrameOutputStart:
		var start fileStart
		if t.active || json.Unmarshal(body, &start) != nil || start.Index != index || start.Size < 0 || start.Size > t.req.MaxArtifactBytes-t.bytes {
			return ErrInvalidResult
		}
		t.size = start.Size
		t.active = true
		t.reader, t.writer = io.Pipe()
		t.done = make(chan outputDone, 1)
		reader, writer, done := t.reader, t.writer, t.done
		go func() {
			file, err := t.req.HostFiles.Put(ctx, index, reader, start.Size)
			_ = reader.CloseWithError(err)
			_ = writer.CloseWithError(err)
			done <- outputDone{file, err}
		}()
		return nil
	case FrameOutputChunk:
		if !t.active || !fileIndex(body, index) || len(body) <= 4 {
			return ErrUnexpectedFrame
		}
		t.bytes += int64(len(body) - 4)
		if t.bytes > t.req.MaxArtifactBytes {
			return ErrOutputLimitExceeded
		}
		_, err := t.writer.Write(body[4:])
		return err
	case FrameOutputEnd:
		if !t.active || !fileIndex(body, index) || len(body) != 4+len("sha256:")+64 {
			return ErrUnexpectedFrame
		}
		_ = t.writer.Close()
		done := <-t.done
		t.done = nil
		t.active = false
		t.writer = nil
		t.reader = nil
		if done.err != nil || done.file.ID != t.req.StoredOutputs[index].ID || done.file.Path != t.req.StoredOutputs[index].Path || done.file.SizeBytes != t.size || done.file.SHA256 != string(body[4:]) {
			return ErrInvalidResult
		}
		t.written = append(t.written, done.file)
		return nil
	}
	return ErrUnexpectedFrame
}
func (t *outputTransfer) validate(result Result) error {
	if len(t.req.StoredOutputs) == 0 {
		for _, a := range result.Artifacts {
			if a.FileID != "" {
				return ErrInvalidResult
			}
		}
	}
	if t.active {
		return ErrInvalidResult
	}
	if result.Status != api.ExecutionStatusSucceeded {
		return nil
	}
	if len(t.written) != len(t.req.StoredOutputs) {
		return ErrInvalidResult
	}
	for i, f := range t.written {
		if i >= len(result.Artifacts) {
			return ErrInvalidResult
		}
		a := result.Artifacts[i]
		if a.FileID != f.ID || a.Name != f.Path || int64(a.SizeBytes) != f.SizeBytes || a.SHA256 != f.SHA256 || len(a.Content) != 0 {
			return ErrInvalidResult
		}
	}
	return nil
}
func guestFiles(ctx context.Context, conn net.Conn, r Request, mu *sync.Mutex) *GuestFileIO {
	return &GuestFileIO{
		Receive: func(ctx context.Context, index int, dst io.Writer) error {
			if index < 0 || index >= len(r.InputFiles) {
				return ErrInvalidRequest
			}
			f := r.InputFiles[index]
			h := sha256.New()
			var n int64
			for {
				kind, body, err := readFrame(ctx, conn)
				if err != nil {
					return err
				}
				if !fileIndex(body, index) {
					return ErrUnexpectedFrame
				}
				if kind == FrameInputEnd {
					if len(body) != 4 || n != f.SizeBytes || "sha256:"+hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
						return ErrInvalidRequest
					}
					return nil
				}
				if kind != FrameInputChunk || len(body) <= 4 {
					return ErrUnexpectedFrame
				}
				n += int64(len(body) - 4)
				if n > f.SizeBytes {
					return ErrInvalidRequest
				}
				_, _ = h.Write(body[4:])
				if err = writeAll(dst, body[4:]); err != nil {
					return err
				}
			}
		},
		Send: func(ctx context.Context, index int, src io.Reader, size int64) (runfile.Descriptor, error) {
			if index < 0 || index >= len(r.StoredOutputs) || size < 0 || size > r.MaxArtifactBytes {
				return runfile.Descriptor{}, ErrInvalidRequest
			}
			f := r.StoredOutputs[index]
			start, _ := json.Marshal(fileStart{index, size})
			mu.Lock()
			defer mu.Unlock()
			if err := writeFrame(ctx, conn, FrameOutputStart, start); err != nil {
				return f, err
			}
			h := sha256.New()
			buf := make([]byte, api.ExecutionFileStreamChunkBytes)
			var n int64
			for {
				count, err := src.Read(buf)
				if count > 0 {
					n += int64(count)
					if n > size {
						return f, ErrInvalidResult
					}
					_, _ = h.Write(buf[:count])
					if err := writeFrame(ctx, conn, FrameOutputChunk, fileChunk(index, buf[:count])); err != nil {
						return f, err
					}
				}
				if err != nil {
					if !errors.Is(err, io.EOF) {
						return f, err
					}
					break
				}
				if count == 0 {
					return f, io.ErrNoProgress
				}
			}
			if n != size {
				return f, ErrInvalidResult
			}
			f.SizeBytes = n
			f.SHA256 = "sha256:" + hex.EncodeToString(h.Sum(nil))
			return f, writeFrame(ctx, conn, FrameOutputEnd, fileChunk(index, []byte(f.SHA256)))
		},
	}
}
