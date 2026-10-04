package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"golang.org/x/sys/unix"
)

// collectArtifacts runs after the interpreter exits and reads only explicitly
// selected regular files. Descriptor-relative traversal rejects every symlink,
// including parent directories; nonblocking opens cannot hang on a FIFO.
func collectArtifacts(ctx context.Context, dir string, names []string, remaining int) ([]api.ExecutionArtifact, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if err := api.ValidateExecutionOutputFiles(names); err != nil {
		return nil, err
	}
	artifacts := make([]api.ExecutionArtifact, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := openArtifact(dir, name)
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(file, int64(max(0, remaining))+1))
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(content) > remaining {
			return nil, executionproto.ErrOutputLimitExceeded
		}
		digest := sha256.Sum256(content)
		artifacts = append(artifacts, api.ExecutionArtifact{Name: name, SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(digest[:]), Content: content})
		if api.ExecutionArtifactsOutputBytes(artifacts) > remaining {
			return nil, executionproto.ErrOutputLimitExceeded
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return artifacts, nil
}

func openArtifact(dir, name string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(fd) }()
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return nil, openErr
		}
		_ = unix.Close(fd)
		fd = next
	}
	leaf, err := unix.Openat(fd, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(leaf), name)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("artifact is not a regular file")
	}
	return file, nil
}
