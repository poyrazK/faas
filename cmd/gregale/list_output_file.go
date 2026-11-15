package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
)

type listOutputReceipt struct {
	Path           string `json:"path,omitempty"`
	Format         string `json:"format"`
	Published      bool   `json:"published"`
	PagesCompleted int    `json:"pages_completed"`
	ItemsEmitted   int    `json:"items_emitted"`
	BytesWritten   int64  `json:"bytes_written"`
	SHA256         string `json:"sha256"`
}

type listOutputSink interface {
	io.WriteCloser
	Sync() error
}

// The root is borrowed from the caller. All names stay relative to its pinned
// directory; publication never follows or replaces the destination.
type listOutputFile struct {
	root      *os.Root
	name      string
	temporary string
	file      listOutputSink
	digest    hash.Hash
	bytes     int64
	writeErr  error
	published bool
	closed    bool
}

func newListOutputFile(root *os.Root, name string) (*listOutputFile, error) {
	if _, err := root.Lstat(name); err == nil {
		return nil, fmt.Errorf("output destination must be a new file: %w", os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect output destination: %w", err)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("choose private output name: %w", err)
	}
	temporary := ".gregale-list-export-" + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("create private output: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		_ = root.Remove(temporary)
		return nil, fmt.Errorf("protect private output: %w", err)
	}
	return &listOutputFile{root: root, name: name, temporary: temporary, file: file, digest: sha256.New()}, nil
}

func (f *listOutputFile) Write(p []byte) (int, error) {
	if f.closed || f.published || f.file == nil {
		return 0, os.ErrClosed
	}
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	n, err := f.file.Write(p)
	if n > 0 {
		_, _ = f.digest.Write(p[:n])
		f.bytes += int64(n)
	}
	if n != len(p) && err == nil {
		err = io.ErrShortWrite
	}
	f.writeErr = err
	return n, err
}

func (f *listOutputFile) Publish(ctx context.Context, progress listStreamProgress) (*listOutputReceipt, error) {
	if f.closed || f.published || f.file == nil {
		return nil, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	if err := f.file.Sync(); err != nil {
		return nil, fmt.Errorf("sync list output: %w", err)
	}
	err := f.file.Close()
	f.file = nil
	if err != nil {
		return nil, fmt.Errorf("close list output: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.root.Link(f.temporary, f.name); err != nil {
		return nil, fmt.Errorf("publish list output without overwrite: %w", err)
	}
	// Link is the commit point. A later cancellation or receipt-write failure
	// must not remove the completed artifact.
	f.published = true
	return &listOutputReceipt{Format: "jsonl", Published: true,
		PagesCompleted: progress.PagesCompleted, ItemsEmitted: progress.ItemsEmitted,
		BytesWritten: f.bytes, SHA256: hex.EncodeToString(f.digest.Sum(nil))}, nil
}

func (f *listOutputFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	var err error
	if f.file != nil {
		err = f.file.Close()
		f.file = nil
	}
	removeErr := f.root.Remove(f.temporary)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(err, removeErr)
}
