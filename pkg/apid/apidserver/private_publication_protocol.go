package apidserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

type privatePublicationReplicaTransport struct {
	server                  *server
	app                     state.App
	target                  api.ToolInvocationTarget
	instance, host, version string
}

func (t privatePublicationReplicaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || r.URL.Path != t.target.EndpointPath {
		return nil, state.ErrInvalidArgument
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, api.MCPProbeMaxRequestBytes+1))
	if err != nil || len(body) > api.MCPProbeMaxRequestBytes {
		return nil, state.ErrInvalidArgument
	}
	if _, ok := api.MCPProbeRPCMethod(body); !ok || !api.MCPProbeRPCMatchesVersion(body, t.version) {
		return nil, state.ErrInvalidArgument
	}
	probe := api.MCPInstanceProtocolProbeRequest{Host: t.host, Endpoint: t.target.EndpointPath, ProtocolVersion: t.version, RPCRequest: body}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	result, problem := t.server.probeMCPInstanceProtocol(r.Context(), t.app, t.target.DeploymentID, t.instance, probe, token)
	if problem != nil || result.BodyTooLarge || result.AppID != t.app.ID || result.DeploymentID != t.target.DeploymentID || result.InstanceID != t.instance {
		return nil, state.ErrConflict
	}
	if result.HTTPStatus >= 200 && result.HTTPStatus < 300 && (result.SessionIDPresent || result.SessionID != "" || result.HTTPStatus != http.StatusAccepted && result.ProtocolVersion != t.version) {
		return nil, state.ErrConflict
	}
	header := make(http.Header)
	header.Set("MCP-Protocol-Version", result.ProtocolVersion)
	header.Set("Content-Type", result.ContentType)
	if result.SessionIDPresent {
		header.Set("MCP-Session-Id", result.SessionID)
	}
	return &http.Response{StatusCode: result.HTTPStatus, Header: header, Body: io.NopCloser(bytes.NewBufferString(result.Body)), Request: r}, nil
}

func (s *server) verifyPrivatePublicationProtocols(ctx context.Context, target api.ToolInvocationTarget, endpoint, token string, contract mcphosting.Contract, legacy bool, wrap func(http.RoundTripper) http.RoundTripper) error {
	ids, err := s.privateReplicaIDs(ctx, target)
	if err != nil {
		return err
	}
	app, err := s.store.AppByID(ctx, target.AppID)
	if err != nil {
		return err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	versions := []string{mcphosting.ProtocolVersion}
	if legacy {
		versions = append(versions, mcphosting.LegacyProtocolVersion)
	}
	for _, version := range versions {
		baseline := contract
		baseline.ProtocolVersion = version
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy, transport.DialContext = nil, oci.EgressDialContext(nil)
		transports := []http.RoundTripper{transport}
		for _, instance := range ids {
			transports = append(transports, privatePublicationReplicaTransport{server: s, app: app, target: target, instance: instance, host: u.Host, version: version})
		}
		for index, next := range transports {
			probe, err := mcphosting.NewClient(endpoint, token, version)
			if err != nil {
				return err
			}
			probe.HTTP = &http.Client{Transport: wrap(next), Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			qualified := mcphosting.ValidateCompatibility(ctx, probe, &baseline).OK
			if index == 0 {
				qualified = qualified && mcphosting.CheckReadiness(ctx, probe, &baseline).OK
			}
			if !qualified {
				return errors.New("private publication protocol qualification failed")
			}
		}
		transport.CloseIdleConnections()
	}
	current, err := s.privateReplicaIDs(ctx, target)
	if err != nil || !slices.Equal(ids, current) {
		return state.ErrConflict
	}
	return nil
}
