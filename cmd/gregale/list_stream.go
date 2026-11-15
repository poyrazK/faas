package main

import (
	"context"
	"encoding/json"
	"io"
)

type listStreamProgress struct {
	PagesCompleted int  `json:"pages_completed"`
	ItemsEmitted   int  `json:"items_emitted"`
	Partial        bool `json:"partial"`
}

type listStreamError struct {
	cause    error
	progress listStreamProgress
}

func (e *listStreamError) Error() string { return e.cause.Error() }
func (e *listStreamError) Unwrap() error { return e.cause }

// Count complete records separately from any bytes written before an output
// failure. Even a truncated first record means stdout is partial.
type listStreamWriter struct {
	dst   io.Writer
	wrote bool
}

func (w *listStreamWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.wrote = true
	}
	if n != len(p) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

func streamListPages[T any](ctx context.Context, dst io.Writer, cursor string, fetch func(context.Context, string) ([]T, string, error)) (listStreamProgress, error) {
	var progress listStreamProgress
	writer := &listStreamWriter{dst: dst}
	encoder := json.NewEncoder(writer)
	_, err := walkListPages(ctx, cursor, true, fetch, func(rows []T) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := encoder.Encode(row); err != nil {
				return err
			}
			progress.ItemsEmitted++
		}
		progress.PagesCompleted++
		return nil
	})
	progress.Partial = err != nil && writer.wrote
	return progress, err
}
