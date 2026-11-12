//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Restore identity reload (ADR-20261031193635 slice 5b). When vmmd restores an
// environment branch from its parent's credential-free capture, it asks the
// resume hook for an identity reload. guest-init fetches the branch's own
// identity document from vmmd, writes it to a tmpfs file only the application
// user can read, and calls the after_restore hook with the file's path and
// digest. The hook must answer with the same digest. The digest is echoed in
// the resume acknowledgement; vmmd cold-boots the branch on any mismatch.

// restoreIdentityReloader is wired at startup when the manifest's
// after_restore hook opts into identity reload. Nil means the guest cannot
// reload its identity, and the resume hook refuses a request for one.
var restoreIdentityReloader atomic.Pointer[func() (string, error)]

var errRestoreIdentityAck = errors.New("after_restore did not acknowledge the restore identity")

type restoreIdentityReload struct {
	hook  api.AfterRestoreHook
	port  int
	uid   int
	fetch func() (runtimeConfigResponse, error)
	write func(path string, uid int, body []byte) error
	call  func(hook api.AfterRestoreHook, port int, extra map[string]string, verify func(*http.Response) error) error
}

func newRestoreIdentityReload(hook api.AfterRestoreHook, port, uid int) restoreIdentityReload {
	return restoreIdentityReload{
		hook: hook, port: port, uid: uid, fetch: fetchRestoreIdentity,
		write: func(path string, uid int, body []byte) error {
			return writePrivateRuntimeSecretFile(path, uid, 0, 0, body)
		},
		call: func(hook api.AfterRestoreHook, port int, extra map[string]string, verify func(*http.Response) error) error {
			if err := hook.Validate(); err != nil {
				return fmt.Errorf("after_restore config: %w", err)
			}
			return callLifecycleHookWith("after_restore", "X-Faas-After-Restore", hook.Path, hook.EffectiveTimeout(), port, extra, verify)
		},
	}
}

// run delivers the identity and returns the digest the hook acknowledged.
func (r restoreIdentityReload) run() (string, error) {
	resp, err := r.fetch()
	if err != nil {
		return "", fmt.Errorf("fetch restore identity: %w", err)
	}
	if resp.Error != "" {
		return "", fmt.Errorf("fetch restore identity: %s", resp.Error)
	}
	var secrets map[string]string
	if resp.Secrets != nil {
		secrets = *resp.Secrets
	}
	body, digest, err := api.RestoreIdentityDocument(resp.Env, secrets)
	if err != nil {
		return "", err
	}
	if !api.ValidRestoreIdentityDigest(resp.Digest) || resp.Digest != digest {
		return "", errors.New("restore identity digest does not match its document")
	}
	if err := r.write(api.RestoreIdentityFilePath, r.uid, body); err != nil {
		return "", fmt.Errorf("write restore identity: %w", err)
	}
	extra := map[string]string{api.AfterRestoreIdentityFileHeader: api.RestoreIdentityFilePath, api.AfterRestoreIdentityDigestHeader: digest}
	verify := func(answer *http.Response) error {
		if answer.Header.Get(api.AfterRestoreIdentityAckHeader) != digest {
			return errRestoreIdentityAck
		}
		return nil
	}
	if err := r.call(r.hook, r.port, extra, verify); err != nil {
		return "", err
	}
	return digest, nil
}

func fetchRestoreIdentity() (runtimeConfigResponse, error) {
	conn, err := dialRuntimeConfigHost()
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	body, err := json.Marshal(runtimeConfigRequest{Kind: api.RuntimeConfigKindIdentity})
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	if err := writeRuntimeConfigFrame(conn, body); err != nil {
		return runtimeConfigResponse{}, err
	}
	frame, err := readRuntimeConfigFrame(conn)
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	var response runtimeConfigResponse
	if err := json.Unmarshal(frame, &response); err != nil {
		return runtimeConfigResponse{}, err
	}
	return response, nil
}
