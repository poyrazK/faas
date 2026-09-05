package oci

import (
	"encoding/json"
	"fmt"
	"io"
)

// Shared OCI image-config raw decoder.
//
// The OCI image-config spec
// (https://github.com/opencontainers/image-spec/blob/main/config.md)
// allows fields at either the top level ("Docker v2 flat") or inside a
// nested "config" envelope ("OCI nested"); in practice some registries
// emit one, some emit the other, and a few emit BOTH. The package's
// two parsers (ParseConfig, parseImageConfig) historically read
// different subsets of fields and preferred the formats differently,
// resulting in drift: the registry path silently dropped Entrypoint and
// User, and the two paths disagreed on Cmd when both envelopes were
// present.
//
// ADR-136 (issue #1186, M-1 commit 3) makes this file the single raw
// decoder. Both callers unmarshal into rawConfig and call resolved()
// for the flat-or-nested preference; new OCI fields get one parser,
// not two.
//
// Reference: ADR-040 (layer symlink policy) is unaffected; ADR-053
// (deploy overrides) operates at a higher layer.

// rawConfig is the unmarshalled-once view of an OCI/Docker
// image-config blob. Pointer-to-nested lets us distinguish "absent"
// from "present-but-empty" for the OCI `config` envelope.
type rawConfig struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	// Flat fields (Docker v2 schema).
	Cmd         []string        `json:"Cmd"`
	Env         []string        `json:"Env"`
	WorkingDir  string          `json:"WorkingDir"`
	Entrypoint  []string        `json:"Entrypoint"`
	User        string          `json:"User"`
	StopSignal  string          `json:"StopSignal"`
	Healthcheck *rawHealthcheck `json:"Healthcheck"`

	// Nested `config` envelope (OCI image-config). Optional — many
	// registry implementations omit it entirely.
	Config *rawNestedConfig `json:"config"`

	// rootfs is the OCI-spec struct (always present in OCI images;
	// absent in pure Docker v2 manifests; tolerate both).
	RootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

// rawNestedConfig is the inner envelope of an OCI image-config.
type rawNestedConfig struct {
	Volumes      map[string]struct{} `json:"Volumes"`
	Cmd          []string            `json:"Cmd"`
	Env          []string            `json:"Env"`
	WorkingDir   string              `json:"WorkingDir"`
	Entrypoint   []string            `json:"Entrypoint"`
	User         string              `json:"User"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts"`
	StopSignal   string              `json:"StopSignal"`
	Healthcheck  *rawHealthcheck     `json:"Healthcheck"`
}

// rawHealthcheck is the unmarshal target for HEALTHCHECK in either
// envelope. The struct fields mirror Docker semantics:
//
//   - Test:     argv for the check command, prefixed by "CMD",
//     "CMD-SHELL", or "NONE" (the only valid Test[0]).
//   - Interval: Docker default 30s.
//   - Timeout:  Docker default 30s.
//   - Retries:  Docker default 3.
//   - StartPeriod: Docker default 0s (no startup grace).
//
// All Durations are encoded as integer seconds at the OCI wire boundary
// (Go's default JSON marshalling for time.Duration is nanoseconds —
// registries consistently emit seconds; we use int and convert at the
// projection site).
type rawHealthcheck struct {
	Test         []string `json:"Test"`
	IntervalS    int      `json:"Interval"`
	TimeoutS     int      `json:"Timeout"`
	Retries      int      `json:"Retries"`
	StartPeriodS int      `json:"StartPeriod"`
}

// rawFields is the resolved single-source-of-truth view: each field is
// the flat value if non-empty, otherwise the nested-`config` value if
// present, otherwise zero.
type rawFields struct {
	Cmd        []string
	Env        []string
	WorkingDir string
	Entrypoint []string
	User       string
}

// resolved applies the flat-then-nested precedence rule. Preserves the
// historical preference (flat wins) so today's deployments don't
// change shape; ADR-136 §Decision 1 records the rationale.
func (r *rawConfig) resolved() rawFields {
	f := rawFields{
		Cmd:        r.Cmd,
		Env:        r.Env,
		WorkingDir: r.WorkingDir,
		Entrypoint: r.Entrypoint,
		User:       r.User,
	}
	if r.Config != nil {
		if len(f.Cmd) == 0 {
			f.Cmd = r.Config.Cmd
		}
		if len(f.Env) == 0 {
			f.Env = r.Config.Env
		}
		if f.WorkingDir == "" {
			f.WorkingDir = r.Config.WorkingDir
		}
		if len(f.Entrypoint) == 0 {
			f.Entrypoint = r.Config.Entrypoint
		}
		if f.User == "" {
			f.User = r.Config.User
		}
	}
	return f
}

// resolvedHealthcheck applies the same flat-then-nested precedence for
// HEALTHCHECK. Pointer semantics matter: a non-nil `flat` wins even if
// empty (mirroring the Docker semantics where `HEALTHCHECK NONE` is an
// explicit override), and the nested envelope only fills the gap when
// flat is nil.
func (r *rawConfig) resolvedHealthcheck() *rawHealthcheck {
	if r.Healthcheck != nil {
		return r.Healthcheck
	}
	if r.Config != nil {
		return r.Config.Healthcheck
	}
	return nil
}

// resolvedStopSignal applies the same flat-then-nested precedence for
// STOPSIGNAL. Empty string is the absence marker (Docker defaults to
// SIGTERM).
func (r *rawConfig) resolvedStopSignal() string {
	if r.StopSignal != "" {
		return r.StopSignal
	}
	if r.Config != nil {
		return r.Config.StopSignal
	}
	return ""
}

// validate returns an error if the rootfs.type is set to anything other
// than "layers" (the only mode the platform supports today).
func (r *rawConfig) validate() error {
	if r.RootFS.Type != "" && r.RootFS.Type != "layers" {
		return fmt.Errorf("oci: unsupported rootfs type %q", r.RootFS.Type)
	}
	return nil
}

// decodeRaw is the single unmarshal site for both ParseConfig and
// parseImageConfig. Callers then project resolved() onto their own
// consumer-facing struct (oci.Config or oci.ImageConfig).
func decodeRaw(r io.Reader) (*rawConfig, error) {
	var raw rawConfig
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("oci: parse config: %w", err)
	}
	return &raw, nil
}
