package mcphosting

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type Report struct {
	Endpoint  string    `json:"endpoint"`
	OK        bool      `json:"ok"`
	Checks    []Check   `json:"checks"`
	Tools     []Tool    `json:"tools,omitempty"`
	Discovery Exchange  `json:"discovery"`
	Stream    *Exchange `json:"stream,omitempty"`
}

// Doctor does not execute tools unless the caller explicitly supplies a
// stream tool. Never infer permission to execute from readOnlyHint annotations.
func Doctor(ctx context.Context, c *Client, legacy bool, streamTool string, args map[string]any) Report {
	report := Report{Endpoint: c.Endpoint, OK: true, Checks: make([]Check, 0)}
	add := func(name string, err error) {
		check := Check{Name: name, Status: "passed"}
		if err != nil {
			check.Status = "failed"
			check.Detail = err.Error()
			report.OK = false
		}
		report.Checks = append(report.Checks, check)
	}
	if c.Token != "" || c.ExpectedAuth != nil {
		add("oauth_resource_discovery", c.VerifyOAuth(ctx))
		if !report.OK {
			return report
		}
	}
	tools, x, err := c.Tools(ctx)
	x.Result = nil // Tool schemas are represented once, in Report.Tools.
	report.Discovery = x
	add("modern_tool_discovery", err)
	if err != nil {
		return report
	}
	report.Tools = tools
	if x.StreamingStatus != "" {
		var streamingErr error
		if x.StreamingStatus != api.StreamingStatusStreaming && x.StreamingStatus != api.StreamingStatusAcceptJSONDowngrade {
			streamingErr = fmt.Errorf("gateway streaming is %s", x.StreamingStatus)
		}
		add("gateway_streaming", streamingErr)
	}
	add("untrusted_origin", c.RejectsUntrustedOrigin(ctx))
	if legacy {
		old, err := NewClient(c.Endpoint, c.Token, LegacyProtocolVersion)
		if err == nil {
			old.HTTP = c.HTTP
			err = old.Initialize(ctx)
			if err == nil {
				_, _, err = old.Tools(ctx)
			}
		}
		add("legacy_stateless_compatibility", err)
	}
	if streamTool != "" {
		var selected *Tool
		for i := range tools {
			if tools[i].Name == streamTool {
				selected = &tools[i]
				break
			}
		}
		if selected == nil {
			add("live_progress", fmt.Errorf("stream probe tool %q not found", streamTool))
			return report
		}
		x, err := c.Call(ctx, *selected, args, true)
		report.Stream = &x
		if err == nil && (x.ContentType != "text/event-stream" || len(x.ProgressMS) < 2 || x.ProgressMS[len(x.ProgressMS)-1]-x.ProgressMS[0] < 100 || x.DurationMS-x.ProgressMS[0] < 100) {
			err = fmt.Errorf("stream probe must send at least two progress events spaced by 100 ms before its final result")
		}
		add("live_progress", err)
	}
	return report
}

// ConnectionConfig uses the common remote-server JSON shape. Clients differ
// in OAuth setup; no token or provider credential is embedded in the output.
func ConnectionConfig(name, endpoint string) map[string]any {
	return map[string]any{"mcpServers": map[string]any{name: map[string]string{"type": "http", "url": endpoint}}}
}
