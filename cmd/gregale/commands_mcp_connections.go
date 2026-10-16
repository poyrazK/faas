package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/mcpmanagement"
)

func cmdMCPConnections(args []string) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "revoke") {
		return printErr("MCP connections", errors.New("use connections list or revoke with --socket"))
	}
	fs := newFlagSet("mcp-connections-"+args[0], flag.ContinueOnError)
	socket := fs.String("socket", "", "owner-protected management control socket")
	limit, offset, grant := 20, 0, ""
	if args[0] == "list" {
		fs.IntVar(&limit, "limit", 20, "page size from 1 to 100")
		fs.IntVar(&offset, "offset", 0, "non-negative page offset")
	} else {
		fs.StringVar(&grant, "grant-id", "", "exact active bridge grant ID to revoke")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *socket == "" || limit < 1 || limit > 100 || offset < 0 || (args[0] == "revoke" && grant == "") {
		return printErr("MCP connections", errors.New("provide --socket, valid page bounds, and --grant-id for revocation"))
	}
	method, path := http.MethodGet, "/connections?limit="+strconv.Itoa(limit)+"&offset="+strconv.Itoa(offset)
	if args[0] == "revoke" {
		method, path = http.MethodDelete, "/connections/"+url.PathEscape(grant)
	}
	if err := requestMCPAdmin(*socket, method, path); err != nil {
		return printErr("MCP connections", err)
	}
	return 0
}

func requestMCPAdmin(socket, method, path string) error {
	client, err := mcpmanagement.AdminClient(socket)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("contact local management bridge: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, mcpmanagement.MaxFrameBytes+1))
	if err != nil {
		return err
	}
	if len(data) > mcpmanagement.MaxFrameBytes {
		return errors.New("admin response exceeds its bound")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("local management control returned HTTP %d", response.StatusCode)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode local management metadata: %w", err)
	}
	return json.NewEncoder(osStdout).Encode(value)
}

func startMCPAdmin(socket string, handler http.Handler) (*http.Server, error) {
	listener, err := mcpmanagement.ListenAdminSocket(socket)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintln(osStderr, "Management MCP control listener stopped unexpectedly")
		}
	}()
	_, _ = fmt.Fprintf(osStderr, "Gregale management MCP control socket %s\n", socket)
	return server, nil
}
