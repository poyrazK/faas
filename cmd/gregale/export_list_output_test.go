package main

import (
	"context"
	"os"
	"syscall"
)

type ListOutputFile = listOutputFile
type ListOutputReceipt = listOutputReceipt

var NewListOutputFile = newListOutputFile

// Deterministic local I/O failures without filling a shared filesystem.
func FailListOutputFile(file *ListOutputFile, phase string, cancel context.CancelFunc) {
	file.file = &listOutputFailure{listOutputSink: file.file, phase: phase, cancel: cancel}
}

type listOutputFailure struct {
	listOutputSink
	phase  string
	cancel context.CancelFunc
}

func (f *listOutputFailure) Write(p []byte) (int, error) {
	if len(p) > 0 && (f.phase == "write" || f.phase == "short") {
		n, err := f.listOutputSink.Write(p[:1])
		if err != nil || f.phase == "short" {
			return n, err
		}
		return n, &os.PathError{Op: "write", Path: "private output", Err: syscall.ENOSPC}
	}
	return f.listOutputSink.Write(p)
}
func (f *listOutputFailure) Sync() error {
	if f.phase == "sync" {
		return &os.PathError{Op: "sync", Path: "private output", Err: syscall.ENOSPC}
	}
	if f.cancel != nil {
		f.cancel()
	}
	return f.listOutputSink.Sync()
}
func (f *listOutputFailure) Close() error {
	err := f.listOutputSink.Close()
	if err == nil && f.phase == "close" {
		err = &os.PathError{Op: "close", Path: "private output", Err: syscall.ENOSPC}
	}
	return err
}
