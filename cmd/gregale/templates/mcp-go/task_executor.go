package main

import (
	"context"
	"errors"
)

func invokeMCPTaskExecutor(execute MCPTaskExecutor, ctx context.Context, args map[string]any, execution *MCPTaskExecutionContext) (result map[string]any, err error) {
	returned := false
	defer func() {
		if !returned {
			// Never format the panic value: application failures can contain
			// customer data. The flag also handles legacy panic(nil) behavior.
			_ = recover()
			result = nil
			err = errors.New("MCP task execution failed")
		}
	}()
	result, err = execute(ctx, args, execution)
	returned = true
	return
}
