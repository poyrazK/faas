// Package secretdgrant connects a compute daemon's storage backend to
// secretd's GrantStorageObjects (ADR-20261117190000 slice 3): with
// FAAS_STORAGE_ACCESS=secretd the node holds no store credential and reaches
// each object through a signed grant for it.
package secretdgrant

// adr: 20261117190000

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagegrant"
	"github.com/onebox-faas/faas/pkg/wire"
)

// AccessEnv selects how a compute daemon reaches the shared store.
const AccessEnv = "FAAS_STORAGE_ACCESS"

// secretdServerName is the name secretd's leaf carries (pkg/pki
// FeatureSecretOpener).
const secretdServerName = "secretd.faas"

// Client implements storage.ObjectGranter over secretd.
type Client struct {
	RPC secretdpb.SecretdClient
}

var _ storage.ObjectGranter = Client{}

// GrantObjects implements storage.ObjectGranter.
func (c Client) GrantObjects(ctx context.Context, requests []storagegrant.Request) ([]storagegrant.Grant, error) {
	req := &secretdpb.GrantStorageObjectsRequest{Objects: make([]*secretdpb.StorageObjectRequest, len(requests))}
	for i, r := range requests {
		req.Objects[i] = &secretdpb.StorageObjectRequest{Key: r.Key, Op: string(r.Op), Generation: r.Generation,
			Etag: r.ETag, UploadId: r.UploadID, PartNumber: r.PartNumber,
			ContentType: r.ContentType, Metadata: r.Metadata}
	}
	resp, err := c.RPC.GrantStorageObjects(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(resp.GetGrants()) != len(requests) {
		return nil, fmt.Errorf("secretd answered %d grants for %d objects", len(resp.GetGrants()), len(requests))
	}
	out := make([]storagegrant.Grant, len(requests))
	for i, g := range resp.GetGrants() {
		if g.GetKey() != requests[i].Key { //nolint:gosec // lengths checked equal above
			return nil, fmt.Errorf("secretd answered grant %d for another key", i)
		}
		expires, err := time.Parse(time.RFC3339, g.GetExpiresAt())
		if err != nil {
			return nil, fmt.Errorf("secretd grant %d: expiry: %w", i, err)
		}
		out[i] = storagegrant.Grant{Method: g.GetMethod(), URL: g.GetUrl(), Headers: g.GetHeaders(), Expires: expires}
	}
	return out, nil
}

// FromEnv applies FAAS_STORAGE_ACCESS to backend. Unset or "direct" keeps
// the daemon's own store credentials; "secretd" dials secretd at
// FAAS_SECRETD_TARGET with the node's leaf and makes every shared store
// under backend use grants. Deletes must already be deferred
// (FAAS_STORAGE_DELETE_MODE=defer). The returned func closes the
// connection.
func FromEnv(ctx context.Context, getenv func(string) string, backend storage.StorageBackend, daemon string, log *slog.Logger) (bool, func(), error) {
	switch mode := getenv(AccessEnv); mode {
	case "", "direct":
		return false, func() {}, nil
	case "secretd":
	default:
		return false, nil, fmt.Errorf("%s: %s=%q: want direct or secretd", daemon, AccessEnv, mode)
	}
	rpc, closeConn, err := dial(ctx, getenv, daemon)
	if err != nil {
		return false, nil, err
	}
	n, err := storage.UseGrants(backend, Client{RPC: rpc}, storeHTTPClient())
	if err == nil && n == 0 {
		err = errors.New("the storage backend has no shared store")
	}
	if err != nil {
		closeConn()
		return false, nil, fmt.Errorf("%s: %s=secretd: %w", daemon, AccessEnv, err)
	}
	if log != nil {
		log.Info(daemon+": shared store reached through secretd grants", "stores", n)
	}
	return true, closeConn, nil
}

func dial(ctx context.Context, getenv func(string) string, daemon string) (secretdpb.SecretdClient, func(), error) {
	target := getenv("FAAS_SECRETD_TARGET")
	if target == "" {
		return nil, nil, fmt.Errorf("%s: %s=secretd needs FAAS_SECRETD_TARGET", daemon, AccessEnv)
	}
	parsed, err := wire.ParseTarget(target)
	if err != nil || parsed.Scheme == wire.SchemeUnix {
		return nil, nil, fmt.Errorf("%s: FAAS_SECRETD_TARGET=%q: want a tcp:// or dns:// target on the control plane", daemon, target)
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("secretd_",
		getenv("FAAS_SECRETD_TLS_CERT_PATH"), getenv("FAAS_SECRETD_TLS_KEY_PATH"), getenv("FAAS_SECRETD_TLS_CA_PATH"))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: load secretd client TLS: %w", daemon, err)
	}
	if tlsCfg == nil {
		return nil, nil, fmt.Errorf("%s: %s=secretd needs FAAS_SECRETD_TLS_{CERT,KEY,CA}_PATH", daemon, AccessEnv)
	}
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = secretdServerName
	}
	conn, err := wire.DialContext(ctx, target, tlsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: dial secretd: %w", daemon, err)
	}
	return secretdpb.NewSecretdClient(conn), func() { _ = conn.Close() }, nil
}

// storeHTTPClient fetches signed URLs: no overall timeout (snapshot objects
// are large and streamed), but bounded connection setup and response wait.
func storeHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 2 * time.Minute,
			MaxIdleConnsPerHost:   32,
			IdleConnTimeout:       90 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		// A signed URL never redirects; following one would carry the grant
		// somewhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
