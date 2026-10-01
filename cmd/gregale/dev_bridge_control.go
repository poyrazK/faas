package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
)

func cmdDevBridgeControl(args []string) int {
	if args[0] == "doctor" {
		return cmdDevBridgeDoctor(args[1:])
	}
	if (args[0] == "list" && len(args) != 1) || (args[0] != "list" && len(args) != 2) {
		return printErr("Invalid bridge command", errors.New("usage: gregale dev bridge list | status SESSION | revoke SESSION"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	switch args[0] {
	case "list":
		out, err := client.ListDevBridges(ctx)
		if err != nil {
			return printErr("Could not list bridges", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		if len(out.Sessions) == 0 {
			_, _ = fmt.Fprintln(osStdout, "No active development bridges.")
		}
		for _, item := range out.Sessions {
			_, _ = fmt.Fprintf(osStdout, "%s  %s  developer=%s  target=%s  expires=%s\n", item.Session.ID, item.ConnectionState, item.Session.Scope.DeveloperID, item.Session.Scope.TargetAppID, item.Session.ExpiresAt.Format(time.RFC3339))
		}
	case "status":
		session, err := client.GetDevBridge(ctx, args[1])
		if err != nil {
			return printErr("Could not inspect bridge", err)
		}
		activity, err := client.GetDevBridgeActivity(ctx, args[1])
		if err != nil {
			return printErr("Could not inspect bridge activity", err)
		}
		out := struct {
			Session  devbridge.Session  `json:"session"`
			Activity devbridge.Activity `json:"activity"`
		}{session, activity}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "Session: %s\nConnection: %s\nDeveloper: %s\nTarget: %s\nExpires: %s\nRecent requests: %d\n", session.ID, activity.ConnectionState, session.Scope.DeveloperID, session.Scope.TargetAppID, session.ExpiresAt.Format(time.RFC3339), len(activity.Requests))
	case "revoke":
		if err := client.RevokeDevBridge(ctx, args[1]); err != nil {
			return printErr("Could not revoke bridge", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]string{"revoked": args[1]}))
		}
		_, _ = fmt.Fprintln(osStdout, "Bridge revoked.")
	}
	return 0
}

func cmdDevBridgeDoctor(args []string) (exit int) {
	if len(args) == 0 {
		return printErr("Missing service", errors.New("usage: gregale dev bridge doctor APP --environment development --local-port 8080"))
	}
	fs := newFlagSet("dev bridge doctor", flag.ContinueOnError)
	environment := fs.String("environment", "development", "named development environment")
	port := fs.Int("local-port", 8080, "local HTTP service port")
	entrypoint := fs.String("entrypoint", "", "remote frontend")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *port < 1 || *port > 65535 {
		return printErr("Invalid doctor options", errors.New("select a valid local port"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	base, err := url.Parse(client.BaseURL())
	if err != nil {
		return printErr("Invalid API URL", err)
	}
	ip := net.ParseIP(base.Hostname())
	if base.Scheme != "https" && (base.Scheme != "http" || ip == nil || !ip.IsLoopback()) {
		return printErr("Invalid API URL", errors.New("bridge credentials require HTTPS"))
	}
	developer, err := loadOrCreateDeveloperID()
	if err != nil {
		return printErr("Developer identity unavailable", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := client.CreateDevBridge(ctx, api.CreateDevBridgeRequest{App: args[0], Environment: *environment, DeveloperID: developer, Entrypoint: *entrypoint})
	if err != nil {
		return printErr("Bridge admission failed; check feature enablement, ownership and environment eligibility", err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := client.RevokeDevBridge(cleanup, session.Session.ID); err != nil {
			_, _ = fmt.Fprintln(osStderr, "Doctor cleanup failed; revoke session "+session.Session.ID)
			if exit == 0 {
				exit = 1
			}
		}
	}()
	endpoint := strings.TrimRight(client.BaseURL(), "/") + "/v1/dev/bridges/" + session.Session.ID + "/status"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return printErr("Invalid relay endpoint", err)
	}
	request.Header.Set(devbridge.AccountHeader, session.Session.Scope.AccountID)
	request.Header.Set(devbridge.TokenHeader, session.Credentials.AttachmentToken)
	probe := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := probe.Do(request)
	if err != nil {
		return printErr("Relay unreachable; check the edge and faas-bridged service", errors.New("relay status probe failed"))
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		return printErr("Relay unavailable; check faas-bridged and its database access", fmt.Errorf("relay returned HTTP %d", response.StatusCode))
	}
	connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(*port)))
	if err != nil {
		return printErr("Local process is not listening; start it or supply a command after --", errors.New("local listener probe failed"))
	}
	_ = connection.Close()
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"ready": true, "dependencies": session.Dependencies}))
	}
	_, _ = fmt.Fprintf(osStdout, "Bridge admission: ready\nRelay: reachable\nLocal listener: ready\nDiscovered dependencies: %d\n", len(session.Dependencies))
	return 0
}
