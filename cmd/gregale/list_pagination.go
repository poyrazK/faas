package main

import (
	"context"
	"fmt"
)

const maxCLIListPages = 1000

// collectListPages buffers results so a failed traversal emits no partial list.
// Cursors are opaque and must advance without repeating.
func collectListPages[T any](ctx context.Context, cursor string, all bool, fetch func(context.Context, string) ([]T, string, error)) ([]T, string, error) {
	items := make([]T, 0)
	next, err := walkListPages(ctx, cursor, all, fetch, func(rows []T) error {
		items = append(items, rows...)
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return items, next, nil
}

// walkListPages visits validated pages in order, sharing the collector's
// cursor and page-budget guards without retaining rows between pages.
func walkListPages[T any](ctx context.Context, cursor string, all bool, fetch func(context.Context, string) ([]T, string, error), visit func([]T) error) (string, error) {
	seen := map[string]bool{cursor: true}
	for page := 0; page < maxCLIListPages; page++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rows, next, err := fetch(ctx, cursor)
		if err != nil {
			return "", err
		}
		if next != "" && seen[next] {
			return "", fmt.Errorf("pagination cursor repeated; list traversal stopped")
		}
		if err := visit(rows); err != nil {
			return "", err
		}
		if !all || next == "" {
			return next, nil
		}
		seen[next] = true
		cursor = next
	}
	return "", fmt.Errorf("pagination exceeded %d pages; use --cursor to fetch smaller ranges", maxCLIListPages)
}

// Offset endpoints end with -1. Every continuation must advance strictly.
func collectOffsetPages[T any](ctx context.Context, offset int, all bool, fetch func(context.Context, int) ([]T, int, error)) ([]T, int, error) {
	items := make([]T, 0)
	for page := 0; page < maxCLIListPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, -1, err
		}
		rows, next, err := fetch(ctx, offset)
		if err != nil {
			return nil, -1, err
		}
		if all && (next < -1 || next >= 0 && next <= offset) {
			return nil, -1, fmt.Errorf("pagination offset did not advance; list traversal stopped")
		}
		items = append(items, rows...)
		if !all || next == -1 {
			return items, next, nil
		}
		offset = next
	}
	return nil, -1, fmt.Errorf("pagination exceeded %d pages; use --offset to fetch smaller ranges", maxCLIListPages)
}
