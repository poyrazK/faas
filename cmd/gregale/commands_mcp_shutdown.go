package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/mcpmanagement"
)

const mcpShutdownGrace = 5 * time.Second

func runMCPStdio(ctx context.Context, s *mcp.Server, lifecycle *mcpmanagement.Lifecycle) error {
	runCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	result := make(chan error, 1)
	go func() { result <- s.Run(runCtx, &mcp.StdioTransport{MaxLineLength: mcpmanagement.MaxFrameBytes}) }()
	var err error
	select {
	case err = <-result:
	case <-ctx.Done():
		err = ctx.Err()
	}
	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), mcpShutdownGrace)
	defer cancel()
	if cleanup := lifecycle.Shutdown(grace); cleanup != nil {
		return fmt.Errorf("management MCP handlers did not finish cleanup: %w", cleanup)
	}
	return err
}

func shutdownMCPHTTP(ctx context.Context, server *http.Server, lifecycle *mcpmanagement.Lifecycle, extra ...*http.Server) error {
	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), mcpShutdownGrace)
	defer cancel()
	lifecycle.BeginDrain()
	done := make(chan struct{}, 1+len(extra))
	servers := append([]*http.Server{server}, extra...)
	for _, current := range servers {
		go func(current *http.Server) {
			if current != nil {
				if current.Shutdown(grace) != nil {
					_ = current.Close()
				}
			}
			done <- struct{}{}
		}(current)
	}
	err := lifecycle.Shutdown(grace)
	for range servers {
		<-done
	}
	if err != nil {
		return fmt.Errorf("management MCP handlers did not finish cleanup: %w", err)
	}
	return nil
}
