package main

import (
	"context"
	"os"
	"time"
)

var ValidateListTimeout = validateListTimeout
var ListReadContext = listReadContext
var ListReadError = listReadError

// Exercise the production publisher's deadline fence without platform services.
func PublishListAfterSyncDeadline(root *os.Root, name string, duration time.Duration) error {
	ctx, cancel := listReadContext(context.Background(), duration)
	defer cancel()
	file, err := newListOutputFile(root, name)
	if err != nil {
		return err
	}
	defer file.Close()
	file.file = &deadlineSyncListSink{listOutputSink: file.file, ctx: ctx}
	_, err = file.Publish(ctx, listStreamProgress{})
	return err
}

type deadlineSyncListSink struct {
	listOutputSink
	ctx context.Context
}

func (s *deadlineSyncListSink) Sync() error { <-s.ctx.Done(); return nil }
