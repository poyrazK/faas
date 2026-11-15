package main

import "context"

func RunListTimeoutTerminalStream(ctx context.Context) int {
	return runStreamList(ctx, "Could not list builds", "", func(context.Context, string) ([]int, string, error) { return []int{1}, "", nil })
}
