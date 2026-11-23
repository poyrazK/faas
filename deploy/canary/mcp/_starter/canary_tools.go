package main

// Canary-only tools added to a freshly generated Go MCP starter by
// scripts/ops/mcp_canary.py. They use the starter's public RegisterMCPTool
// hook, so the canary exercises the starter exactly as customers receive it.
// It lives under _starter/ because it builds only inside the starter: Go
// tooling skips directories whose names begin with an underscore.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

var canaryStarted = time.Now()

// One shared keep-alive client on purpose: customer tools pool upstream
// connections, and the canary must notice when pooled sockets break across a
// park/wake (ADR-20261121160000 #5).
var canaryHTTP = &http.Client{Timeout: 10 * time.Second}

func canaryObject(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func canaryResult(structured map[string]any) map[string]any {
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": fmt.Sprint(structured)}},
		"structuredContent": structured,
	}
}

func init() {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(RegisterMCPTool(map[string]any{
		"name":        "canary_sleep",
		"description": "Wait without writing anything, then answer (long silent tool call).",
		"inputSchema": canaryObject(map[string]any{
			"seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 120},
		}, "seconds"),
		"outputSchema": canaryObject(map[string]any{"slept_ms": map[string]any{"type": "integer"}}, "slept_ms"),
	}, func(ctx context.Context, args map[string]any, _ *MCPRequestContext) (map[string]any, error) {
		seconds, _ := args["seconds"].(float64)
		start := time.Now()
		select {
		case <-time.After(time.Duration(seconds * float64(time.Second))):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return canaryResult(map[string]any{"slept_ms": time.Since(start).Milliseconds()}), nil
	}))

	must(RegisterMCPTool(map[string]any{
		"name":        "canary_fetch",
		"description": "GET an HTTPS URL from inside the app; connect_ip pins the TCP destination (it must be one of the host's DNS answers) while keeping SNI and Host.",
		"inputSchema": canaryObject(map[string]any{
			"url":        map[string]any{"type": "string"},
			"connect_ip": map[string]any{"type": "string"},
		}, "url"),
		"outputSchema": canaryObject(map[string]any{
			"status": map[string]any{"type": "integer"},
			"ms":     map[string]any{"type": "integer"},
			"error":  map[string]any{"type": "string"},
		}, "status", "ms", "error"),
	}, func(ctx context.Context, args map[string]any, _ *MCPRequestContext) (map[string]any, error) {
		raw, _ := args["url"].(string)
		target, err := url.Parse(raw)
		if err != nil || target.Scheme != "https" || target.Host == "" {
			return nil, errors.New("url must be an absolute https URL")
		}
		client := canaryHTTP
		if ip, _ := args["connect_ip"].(string); ip != "" {
			pinned := net.ParseIP(ip)
			if pinned == nil {
				return nil, errors.New("connect_ip must be an IP address")
			}
			// Apps reach only addresses they resolved: the VM's egress
			// firewall admits a destination after the app's own DNS lookup
			// returns it. Resolve the host as any app would, and require
			// the pinned address to be one of the answers.
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, target.Hostname())
			if err != nil {
				return canaryResult(map[string]any{"status": 0, "ms": 0, "error": "resolve " + target.Hostname() + ": " + err.Error()}), nil
			}
			found := false
			for _, a := range addrs {
				found = found || a.IP.Equal(pinned)
			}
			if !found {
				return canaryResult(map[string]any{"status": 0, "ms": 0, "error": target.Hostname() + " does not resolve to " + ip}), nil
			}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.DisableKeepAlives = true
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip, "443"))
			}
			client = &http.Client{Timeout: 10 * time.Second, Transport: transport}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, errors.New("could not build request")
		}
		start := time.Now()
		response, err := client.Do(request)
		elapsed := time.Since(start).Milliseconds()
		if err != nil {
			return canaryResult(map[string]any{"status": 0, "ms": elapsed, "error": err.Error()}), nil
		}
		_ = response.Body.Close()
		return canaryResult(map[string]any{"status": response.StatusCode, "ms": elapsed, "error": ""}), nil
	}))

	must(RegisterMCPTool(map[string]any{
		"name":        "canary_whoami",
		"description": "Report this process's identity and clock.",
		"inputSchema": canaryObject(map[string]any{}),
		"outputSchema": canaryObject(map[string]any{
			"pid":         map[string]any{"type": "integer"},
			"uptime_s":    map[string]any{"type": "integer"},
			"now_unix_ms": map[string]any{"type": "integer"},
		}, "pid", "uptime_s", "now_unix_ms"),
	}, func(context.Context, map[string]any, *MCPRequestContext) (map[string]any, error) {
		return canaryResult(map[string]any{
			"pid":         os.Getpid(),
			"uptime_s":    int64(time.Since(canaryStarted).Seconds()),
			"now_unix_ms": time.Now().UnixMilli(),
		}), nil
	}))
}
