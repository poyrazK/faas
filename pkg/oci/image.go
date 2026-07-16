package oci

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// This package models just enough of the OCI image spec to power the two-drive
// scheme (spec §4.6): given an app image built FROM one of our base images, find
// the layers that sit ABOVE the base — those, and only those, become the per-app
// drive1 layer. The shared base is drive0, counted once. Extracting the whole
// image per app would duplicate ~150 MB of base each time and destroy the
// 130 MB fleet-snapshot economics.

// Layer is one filesystem layer, identified by its uncompressed digest (DiffID).
type Layer struct {
	DiffID    string // sha256:… of the uncompressed tar (rootfs.diff_ids)
	Digest    string // sha256:… of the compressed blob (optional; manifest side)
	MediaType string
	Size      int64
}

// Config is the subset of the OCI image config we need: the exec contract and
// the ordered diff_ids that identify each layer bottom-to-top.
type Config struct {
	Env        []string // "KEY=VALUE" entries
	Entrypoint []string
	Cmd        []string
	WorkingDir string
	User       string
	DiffIDs    []string // rootfs.diff_ids, bottom-to-top
}

// ociConfigJSON matches the on-disk OCI image config document.
type ociConfigJSON struct {
	Config struct {
		Env        []string `json:"Env"`
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
		WorkingDir string   `json:"WorkingDir"`
		User       string   `json:"User"`
	} `json:"config"`
	RootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

// ParseConfig reads an OCI image config JSON document.
func ParseConfig(r io.Reader) (Config, error) {
	var doc ociConfigJSON
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return Config{}, fmt.Errorf("oci: parse config: %w", err)
	}
	if doc.RootFS.Type != "" && doc.RootFS.Type != "layers" {
		return Config{}, fmt.Errorf("oci: unsupported rootfs type %q", doc.RootFS.Type)
	}
	return Config{
		Env:        doc.Config.Env,
		Entrypoint: doc.Config.Entrypoint,
		Cmd:        doc.Config.Cmd,
		WorkingDir: doc.Config.WorkingDir,
		User:       doc.Config.User,
		DiffIDs:    doc.RootFS.DiffIDs,
	}, nil
}

// LayersAboveBase returns the app's diff_ids that sit above the base image. It
// requires the base's diff_ids to be an exact prefix of the app's — i.e. the app
// really was built FROM base. Otherwise the two-drive assumption is violated and
// we must not proceed (a mismatched base would produce a broken overlay).
func LayersAboveBase(baseDiffIDs, appDiffIDs []string) ([]string, error) {
	if len(baseDiffIDs) > len(appDiffIDs) {
		return nil, fmt.Errorf("oci: base has more layers (%d) than app (%d)", len(baseDiffIDs), len(appDiffIDs))
	}
	for i, d := range baseDiffIDs {
		if appDiffIDs[i] != d {
			return nil, fmt.Errorf("oci: app not built FROM base: layer %d differs (base %s, app %s)", i, short(d), short(appDiffIDs[i]))
		}
	}
	above := appDiffIDs[len(baseDiffIDs):]
	if len(above) == 0 {
		return nil, fmt.Errorf("oci: app has no layers above the base (empty app layer)")
	}
	// Return a copy so callers can't mutate the app's slice.
	out := make([]string, len(above))
	copy(out, above)
	return out, nil
}

// ManifestFromConfig derives the guest app.json contract from the OCI config
// (spec §4.6: imaged injects /etc/faas/app.json). Entrypoint is Entrypoint+Cmd
// per OCI semantics; Env is flattened to a map; User maps to the guest user.
func ManifestFromConfig(cfg Config) (api.AppManifest, error) {
	argv := append(append([]string{}, cfg.Entrypoint...), cfg.Cmd...)
	m := api.AppManifest{
		Entrypoint: argv,
		Env:        envSliceToMap(cfg.Env),
		WorkingDir: cfg.WorkingDir,
		User:       normalizeUser(cfg.User),
	}
	if err := m.Validate(); err != nil {
		return api.AppManifest{}, fmt.Errorf("oci: derive manifest: %w", err)
	}
	return m, nil
}

// envSliceToMap converts OCI "KEY=VALUE" entries to a map. Later entries win, and
// an entry with no '=' is treated as KEY="" (OCI-permissive).
func envSliceToMap(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			m[e] = ""
			continue
		}
		m[k] = v
	}
	return m
}

// normalizeUser maps an OCI User field to a guest user name. A bare numeric uid
// that matches the default app uid is normalised to the default user name; empty
// stays empty (guest-init applies its default).
func normalizeUser(user string) string {
	if user == "" {
		return ""
	}
	if n, err := strconv.Atoi(user); err == nil && n == api.DefaultAppUID {
		return api.DefaultAppUser
	}
	// Strip an optional group ("user:group") — guest-init only needs the user.
	if u, _, ok := strings.Cut(user, ":"); ok {
		return u
	}
	return user
}

func short(digest string) string {
	d := strings.TrimPrefix(digest, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
