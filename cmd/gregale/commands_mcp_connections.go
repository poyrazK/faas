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
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcpmanagement"
)

// cmdMCPConnections lists and revokes the agents connected to the account
// (ADR-20261030133644). With --socket it is the operator's view of a
// bridge's grants through its local admin socket.
func cmdMCPConnections(args []string) int {
	if len(args) > 0 && args[0] == "activity" {
		return cmdMCPConnectionActivity(args[1:])
	}
	if len(args) == 0 || (args[0] != "list" && args[0] != "revoke") {
		return printErr("MCP connections", errors.New("use connection list, connection activity <id> | --self, or connection revoke <id> | --all"))
	}
	fs := newFlagSet("mcp-connections-"+args[0], flag.ContinueOnError)
	socket := fs.String("socket", "", "owner-protected management control socket (operators)")
	org := fs.String("org", "", "an organization's agents, for its owners and admins (ADR-20261101225122)")
	limit, offset, grant, all := 20, 0, "", false
	if args[0] == "list" {
		fs.IntVar(&limit, "limit", 20, "page size from 1 to 100 (with --socket)")
		fs.IntVar(&offset, "offset", 0, "non-negative page offset (with --socket)")
	} else {
		fs.StringVar(&grant, "grant-id", "", "exact active bridge grant ID to revoke (with --socket)")
		fs.BoolVar(&all, "all", false, "revoke every agent connected to the account")
	}
	flags, positional := splitArgsForFlags(args[1:], "all")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if *socket == "" {
		return cmdMCPAgentConnections(args[0], positional, all, *org)
	}
	if len(positional) != 0 || all || *org != "" || limit < 1 || limit > 100 || offset < 0 || (args[0] == "revoke" && grant == "") {
		return printErr("MCP connections", errors.New("with --socket, provide valid page bounds, and --grant-id for revocation"))
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

// agentConnectionCalls are the account's connected-agents calls, or one
// organization's with --org (ADR-20261101225122).
type agentConnectionCalls struct {
	list      func(context.Context) (api.AgentConnectionsResponse, error)
	revoke    func(context.Context, string) error
	revokeAll func(context.Context) (api.AgentConnectionsRevokeAllResponse, error)
}

func connectionCalls(client *Client, org string) agentConnectionCalls {
	if org == "" {
		return agentConnectionCalls{client.ListAgentConnections, client.RevokeAgentConnection, client.RevokeAllAgentConnections}
	}
	return agentConnectionCalls{
		list: func(ctx context.Context) (api.AgentConnectionsResponse, error) {
			return client.ListOrgAgentConnections(ctx, org)
		},
		revoke: func(ctx context.Context, id string) error { return client.RevokeOrgAgentConnection(ctx, org, id) },
		revokeAll: func(ctx context.Context) (api.AgentConnectionsRevokeAllResponse, error) {
			return client.RevokeAllOrgAgentConnections(ctx, org)
		},
	}
}

// cmdMCPAgentConnections lists or revokes the account's connected agents, or
// an organization's.
func cmdMCPAgentConnections(verb string, ids []string, all bool, org string) int {
	if len(ids) > 1 || (verb == "list" && len(ids) != 0) || (verb == "revoke" && all == (len(ids) == 1)) {
		return printErr("MCP connections", errors.New("use connection list, or connection revoke <id> | --all"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, calls := context.Background(), connectionCalls(client, org)
	switch {
	case verb == "list":
		out, err := calls.list(ctx)
		if err != nil {
			return printErr("Could not list connected agents", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		printAgentConnections(out.Items)
	case all:
		out, err := calls.revokeAll(ctx)
		if err != nil {
			return printErr("Could not revoke connected agents", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		PrintOK(osStdout, "Revoked %d connected agent(s)", out.Revoked)
	default:
		if err := calls.revoke(ctx, ids[0]); err != nil {
			return printErr("Could not revoke connected agent", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"id": ids[0], "revoked": true}))
		}
		PrintOK(osStdout, "Revoked; the agent must sign in again")
	}
	return 0
}

func printAgentConnections(items []api.AgentConnectionResponse) {
	if len(items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No agents are connected to this account.")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "%-36s  %-10s  %-32s  %-20s  %-20s  %-24s  %-24s  %s\n", "ID", "KIND", "AGENT", "LAST USED", "EXPIRES", "MEMBER", "PROJECTS", "APPROVALS")
	for _, c := range items {
		kind, agent := "agent key", c.Label
		if c.Kind == api.AgentConnectionHostedMCP {
			kind, agent = "hosted MCP", c.ClientName
			if c.ClientSite != "" {
				agent += " (" + c.ClientSite + ")"
			}
		}
		projects := "all"
		if len(c.Projects) > 0 {
			projects = strings.Join(c.Projects, ",")
		}
		approvals := "none"
		if len(c.Approvals) > 0 {
			approvals = strings.Join(c.Approvals, ",")
		}
		_, _ = fmt.Fprintf(osStdout, "%-36s  %-10s  %-32s  %-20s  %-20s  %-24s  %-24s  %s\n", c.ID, kind, dashIfEmpty(agent), dashIfEmpty(c.LastUsedAt), dashIfEmpty(c.ExpiresAt), dashIfEmpty(c.Member), projects, approvals)
	}
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
