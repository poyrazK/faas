package oci

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

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
	Env        map[string]string // flattened "KEY=VALUE" entries
	Entrypoint []string
	Cmd        []string
	WorkingDir string
	User       string
	// Healthcheck mirrors the OCI HEALTHCHECK shape; nil if absent
	// (the field is only populated when the image declares one).
	// Runtime wiring of the polling loop lands in M-2 (ADR-X5).
	Healthcheck *ImageHealthcheck
	// StopSignal mirrors OCI STOPSIGNAL (default "SIGTERM").
	// Runtime wiring in M-2 (ADR-X3 lifecycle contract).
	StopSignal string
	// StopGracePeriodS mirrors OCI StopGracePeriodSeconds. Runtime
	// wiring in M-2.
	StopGracePeriodS int
	DiffIDs          []string // rootfs.diff_ids, bottom-to-top
}

// ImageHealthcheck is the OCI HEALTHCHECK shape projected onto a
// platform-friendly type. Durations are seconds at the wire boundary
// (registries consistently emit integer seconds; conversion to time.Duration
// happens at the AppManifest projection site, not here).
//
// Test[0] is "CMD", "CMD-SHELL", or "NONE" per Docker semantics; the
// remaining Test entries are argv to the check command. Empty Test
// means the image didn't declare one.
//
// Field semantics (Docker defaults):
//   - Interval     30s — poll cadence after StartPeriod.
//   - Timeout      30s — per-probe HTTP exec timeout.
//   - Retries       3  — consecutive failure count to mark unhealthy.
//   - StartPeriod   0s — startup grace during which failures don't
//     count (Docker 17.05+).
//
// ADR-136 §Decision 3 records the rationale for surfacing these.
type ImageHealthcheck struct {
	Test         []string
	IntervalS    int
	TimeoutS     int
	Retries      int
	StartPeriodS int
}

// ParseConfig reads an OCI/Docker image config JSON document.
//
// Behaviour:
//   - Accepts both Docker v2 flat fields (top-level Cmd/Env/etc.) and
//     the nested `config` envelope (OCI image-config). Flat values win
//     when both are present (preserves historical preference; see
//     ADR-136 §Decision 1).
//   - rootfs.type, when set, must be "layers"; anything else is
//     rejected (we don't model raw single-blob rootfs).
//
// The shared raw decoder lives in oci.go (rawConfig); this function
// projects the resolved fields onto the rich oci.Config projection.
func ParseConfig(r io.Reader) (Config, error) {
	raw, err := decodeRaw(r)
	if err != nil {
		return Config{}, err
	}
	if err := raw.validate(); err != nil {
		return Config{}, err
	}
	f := raw.resolved()
	return Config{
		Env:              envSliceToMap(f.Env),
		Entrypoint:       f.Entrypoint,
		Cmd:              f.Cmd,
		WorkingDir:       f.WorkingDir,
		User:             f.User,
		Healthcheck:      healthcheckFromRaw(raw.resolvedHealthcheck()),
		StopSignal:       raw.resolvedStopSignal(),
		StopGracePeriodS: stopGraceFromRaw(raw),
		DiffIDs:          raw.RootFS.DiffIDs,
	}, nil
}

// LayersAboveBase returns the app's diff_ids that sit above the base image. It
// requires the base's diff_ids to be an exact prefix of the app's — i.e. the app
// really was built FROM base. Otherwise the two-drive assumption is violated and
// we must not proceed (a mismatched base would produce a broken overlay).
//
// ADR-141 §Decision 3: when the strict-prefix check fails, the function
// returns ErrLayersNotAboveBase via fmt.Errorf("%w: …") so the imaged
// dispatch in buildImageLayer can branch on errors.Is. Today the
// dispatch surfaces this as today-equivalent failure (customers must
// `faas deploy --full-rootfs` to opt into the full-rootfs path).
// Auto-fallback on paid plans lands in commit 6.
func LayersAboveBase(baseDiffIDs, appDiffIDs []string) ([]string, error) {
	if len(baseDiffIDs) > len(appDiffIDs) {
		return nil, fmt.Errorf("oci: base has more layers (%d) than app (%d)", len(baseDiffIDs), len(appDiffIDs))
	}
	for i, d := range baseDiffIDs {
		if appDiffIDs[i] != d {
			return nil, fmt.Errorf("%w: app not built FROM base: layer %d differs (base %s, app %s)",
				ErrLayersNotAboveBase, i, short(d), short(appDiffIDs[i]))
		}
	}
	above := appDiffIDs[len(baseDiffIDs):]
	if len(above) == 0 {
		return nil, fmt.Errorf("%w: app has no layers above the base (empty app layer)",
			ErrLayersNotAboveBase)
	}
	// Return a copy so callers can't mutate the app's slice.
	out := make([]string, len(above))
	copy(out, above)
	return out, nil
}

// ManifestFromConfig derives the guest app.json contract from the OCI config
// (spec §4.6: imaged injects /etc/faas/app.json). Entrypoint is Entrypoint+Cmd
// per OCI semantics; Env is flattened to a map; User maps to the guest user.
// Healthcheck/StopSignal/StopGracePeriod are surfaced onto the AppManifest
// additively (ADR-136 §Decision 3-4) — runtime wiring of those fields lands
// in M-2; this function only projects the wire shape.
func ManifestFromConfig(cfg Config) (api.AppManifest, error) {
	if len(cfg.Entrypoint) == 0 && len(cfg.Cmd) == 0 {
		return api.AppManifest{}, fmt.Errorf("%w: image declares neither Entrypoint nor Cmd", ErrImageManifestInvalid)
	}
	argv := append(append([]string{}, cfg.Entrypoint...), cfg.Cmd...)
	// Short-circuit on argv[0]=="" with the canonical sentinel — a
	// registry manifest whose Entrypoint/Cmd contains an empty string
	// at index 0 is the same shape of failure as "neither declared",
	// and must surface as ErrImageManifestInvalid so the imaged
	// handler persists the canonical code (api.CodeImageManifestInvalid)
	// rather than the plain-string Validate() error. Same wrap
	// pattern as the "neither Entrypoint nor Cmd" guard above.
	if len(argv) > 0 && argv[0] == "" {
		return api.AppManifest{}, fmt.Errorf("%w: argv[0] is empty (Entrypoint[0]=%q, Cmd[0]=%q)", ErrImageManifestInvalid, safeHead(cfg.Entrypoint), safeHead(cfg.Cmd))
	}
	m := api.AppManifest{
		Entrypoint: argv,
		Env:        cfg.Env, // already a map at this layer; ParseConfig flattens once
		WorkingDir: cfg.WorkingDir,
		User:       normalizeUser(cfg.User),
	}
	if cfg.Healthcheck != nil {
		m.Healthcheck = &api.AppManifestHealthcheck{
			Test:         append([]string(nil), cfg.Healthcheck.Test...),
			IntervalS:    cfg.Healthcheck.IntervalS,
			TimeoutS:     cfg.Healthcheck.TimeoutS,
			Retries:      cfg.Healthcheck.Retries,
			StartPeriodS: cfg.Healthcheck.StartPeriodS,
		}
	}
	if cfg.StopSignal != "" {
		m.StopSignal = cfg.StopSignal
	}
	if cfg.StopGracePeriodS > 0 {
		m.StopGracePeriod = time.Duration(cfg.StopGracePeriodS) * time.Second
	}
	if err := m.Validate(); err != nil {
		return api.AppManifest{}, fmt.Errorf("oci: derive manifest: %w", err)
	}
	return m, nil
}

// safeHead returns the first element of s or "" if s is empty. Used
// only for diagnostic formatting in the ErrImageManifestInvalid
// error string — never used as a guard.
func safeHead(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
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

// healthcheckFromRaw projects a rawHealthcheck onto the public
// ImageHealthcheck. Returns nil when the raw is nil (image did not
// declare a HEALTHCHECK).
func healthcheckFromRaw(r *rawHealthcheck) *ImageHealthcheck {
	if r == nil {
		return nil
	}
	return &ImageHealthcheck{
		Test:         append([]string(nil), r.Test...),
		IntervalS:    r.IntervalS,
		TimeoutS:     r.TimeoutS,
		Retries:      r.Retries,
		StartPeriodS: r.StartPeriodS,
	}
}

// stopGraceFromRaw reads the OCI-spec StopGracePeriodSeconds value.
// The OCI image-config spec only carries StopSignal; StopGracePeriod
// lives on the OCI *runtime* spec, not the image spec. This helper
// returns 0 today and exists so M-2's lifecycle ADR can wire a
// platform-side source (operator override, per-plan cap, etc.) into
// the projection without re-decoding the raw config. ADR-136 §Decision
// 3 records the carve-out.
func stopGraceFromRaw(_ *rawConfig) int {
	return 0
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
