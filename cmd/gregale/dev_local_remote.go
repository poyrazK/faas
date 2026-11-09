package main

// Remote resources for `gregale dev --local` (ADR-20261107210000 slice 2):
// local functions use the developer session's Postgres and buckets. Owner
// decisions 2026-11-07: Postgres through the task-VM tunnel by default,
// a bucket credential minted per local session and revoked on exit, and
// secrets stay sealed — only their missing names are reported.

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// devLocalRemote is what a local session borrowed from the remote one.
type devLocalRemote struct {
	App     string
	Env     map[string]string
	notes   []string
	cleanup []func()
	// remoteKeys are the env and secret names the dev app has.
	remoteKeys []string
}

func (r *devLocalRemote) note(format string, args ...any) {
	r.notes = append(r.notes, fmt.Sprintf(format, args...))
}

// close revokes credentials and closes tunnels, newest first.
func (r *devLocalRemote) close() {
	for i := len(r.cleanup) - 1; i >= 0; i-- {
		r.cleanup[i]()
	}
	r.cleanup = nil
}

// attachDevLocalRemote connects to the source directory's developer session.
// Every failure degrades to a note: local functions still run without it.
func attachDevLocalRemote(ctx context.Context, sourcePath string, progress io.Writer) *devLocalRemote {
	r := &devLocalRemote{Env: map[string]string{}}
	target, terr := resolveDevTarget(sourcePath, "")
	if terr != nil {
		r.note("Remote resources: off (%v)", terr.err)
		return r
	}
	client, err := authedClient()
	if err != nil {
		r.note("Remote resources: off (not logged in; run gregale login to use your developer session's database and buckets)")
		return r
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	session, err := client.GetDevSession(lookupCtx, target.Project, target.WorkspaceID)
	cancel()
	if isNotFound(err) {
		r.note("Remote resources: off (no developer environment for %s from this directory; start one with gregale dev to use its database and buckets)", target.Project)
		return r
	}
	if err != nil {
		r.note("Remote resources: off (%v)", err)
		return r
	}
	r.App = session.App.Slug
	if session.Postgres != nil {
		r.attachPostgres(ctx, client, *session.Postgres, progress)
	}
	for _, b := range session.Buckets {
		r.attachBucket(ctx, client, b)
	}
	if list, err := client.GetAppsSlugEnv(ctx, r.App); err == nil {
		for _, e := range list.Env {
			r.remoteKeys = append(r.remoteKeys, e.Key)
		}
	}
	return r
}

func (r *devLocalRemote) attachPostgres(ctx context.Context, client *api.Client, pg api.DevPostgresResponse, progress io.Writer) {
	if pg.BindingState != "ready" || pg.EnvironmentKey == "" {
		r.note("Postgres %s: binding is %s; not connected", pg.Name, pg.BindingState)
		return
	}
	_, _ = fmt.Fprintf(progress, "Connecting to Postgres %s through %s (starts a task VM)...\n", pg.Name, r.App)
	dsn, err := readAppTaskEnv(ctx, client, r.App, pg.EnvironmentKey, appExecInteractiveStartupWait)
	if err != nil {
		r.note("Postgres %s: not connected (%v); app tasks must be on your plan", pg.Name, err)
		return
	}
	target, err := parsePostgresConnectTarget(dsn)
	if err != nil {
		r.note("Postgres %s: not connected (%v)", pg.Name, err)
		return
	}
	tunnel, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.note("Postgres %s: not connected (%v)", pg.Name, err)
		return
	}
	session, closeSession, err := openAppTaskStreamSession(ctx, client, r.App, []string{api.AppTaskPortForwardCommand, target.hostPort}, appExecInteractiveStartupWait)
	if err != nil {
		_ = tunnel.Close()
		r.note("Postgres %s: not connected (%v)", pg.Name, err)
		return
	}
	relayLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = tunnel.Close()
		closeSession()
		r.note("Postgres %s: not connected (%v)", pg.Name, err)
		return
	}
	pgCtx, cancel := context.WithCancel(ctx)
	go func() { _ = forwardAppTaskSession(pgCtx, tunnel, session, session.stdout, closeSession) }()
	go pgRelay{upstream: tunnel.Addr().String(), host: target.host, sslMode: target.sslMode}.serve(pgCtx, relayLn)
	r.cleanup = append(r.cleanup, func() {
		cancel()
		_ = relayLn.Close()
		_ = tunnel.Close()
		_ = session.CloseStdin()
		closeSession()
	})
	r.Env[pg.EnvironmentKey] = localPostgresURL(target, relayLn.Addr().(*net.TCPAddr).Port)
	r.note("Postgres %s: %s points at a local tunnel (TLS to the database is verified as its binding asks)", pg.Name, pg.EnvironmentKey)
}

func (r *devLocalRemote) attachBucket(ctx context.Context, client *api.Client, b api.DevBucketResponse) {
	permission := b.Binding.Credential.Permission
	if permission == "" {
		permission = api.ObjectBucketPermissionReadWrite
	}
	host, _ := os.Hostname()
	label := "gregale dev --local " + host
	if len(label) > 64 {
		label = label[:64]
	}
	cred, err := client.CreateObjectS3Credential(ctx, r.App, b.Bucket.ID, api.CreateObjectS3CredentialRequest{Label: label, Permission: permission})
	if err != nil {
		r.note("Bucket %s: not connected (%v)", b.Bucket.Name, err)
		return
	}
	keys := b.Binding.SecretKeys
	for k, v := range map[string]string{
		keys.Endpoint: cred.Endpoint, keys.Region: cred.Region, keys.Bucket: b.Bucket.Name,
		keys.AccessKeyID: cred.AccessKeyID, keys.SecretAccessKey: cred.SecretAccessKey, keys.AddressingStyle: cred.AddressingStyle,
	} {
		if k != "" {
			r.Env[k] = v
		}
	}
	bucketID, credID := b.Bucket.ID, cred.ID
	r.cleanup = append(r.cleanup, func() {
		revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := client.RevokeObjectS3Credential(revokeCtx, r.App, bucketID, credID); err != nil {
			_, _ = fmt.Fprintf(osStderr, "! could not revoke the local credential %s on bucket %s: %v; revoke it with the API\n", credID, b.Bucket.Name, err)
		}
	})
	r.note("Bucket %s: %s_* use a credential for this session, revoked when you stop", b.Bucket.Name, strings.TrimSuffix(keys.Endpoint, "_ENDPOINT"))
}

// missingSecrets lists the dev app's env and secret names that no local
// source supplies. Values never leave Gregale.
func (r *devLocalRemote) missingSecrets(have func(string) bool) []string {
	var missing []string
	for _, k := range r.remoteKeys {
		if _, injected := r.Env[k]; injected || have(k) {
			continue
		}
		missing = append(missing, k)
	}
	sort.Strings(missing)
	return missing
}
