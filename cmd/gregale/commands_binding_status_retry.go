package main

import "context"

type bindingStatusReadRetryKey struct{}

func withBindingStatusReadRetries(ctx context.Context, retries *int) context.Context {
	return context.WithValue(ctx, bindingStatusReadRetryKey{}, retries)
}
func bindingStatusReadRetries(ctx context.Context) *int {
	value, _ := ctx.Value(bindingStatusReadRetryKey{}).(*int)
	return value
}
