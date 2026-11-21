package main

import (
	"context"
	"io"
)

type ListStreamProgress = listStreamProgress

func StreamListPages[T any](ctx context.Context, dst io.Writer, cursor string, fetch func(context.Context, string) ([]T, string, error)) (ListStreamProgress, error) {
	return streamListPages(ctx, dst, cursor, fetch)
}

func RunStreamList[T any](ctx context.Context, title, cursor string, fetch func(context.Context, string) ([]T, string, error)) int {
	return runStreamList(ctx, title, cursor, fetch)
}
