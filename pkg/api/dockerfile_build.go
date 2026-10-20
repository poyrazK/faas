package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// DockerfileBuildOptions are a source-built Compose service's build arguments
// and target stage (ADR-20261019142544). They apply only to Dockerfile builds.
type DockerfileBuildOptions struct {
	Args   map[string]string `json:"args,omitempty"`
	Target string            `json:"target,omitempty"`
}

var (
	dockerfileBuildArgName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// BuildKit lowercases stage names; the grammar matches its stage parser.
	dockerfileBuildStageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
)

func (o *DockerfileBuildOptions) Clone() *DockerfileBuildOptions {
	if o == nil {
		return nil
	}
	return &DockerfileBuildOptions{Args: maps.Clone(o.Args), Target: o.Target}
}

// IsZero reports options that change nothing about the build.
func (o *DockerfileBuildOptions) IsZero() bool {
	return o == nil || (len(o.Args) == 0 && o.Target == "")
}

// TargetStage returns the target stage, or "" for the final stage.
func (o *DockerfileBuildOptions) TargetStage() string {
	if o == nil {
		return ""
	}
	return o.Target
}

// ArgNames returns the argument names, sorted.
func (o *DockerfileBuildOptions) ArgNames() []string {
	if o == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(o.Args))
}

// Validate checks names, sizes and the target grammar against
// pkg/api/limits.go.
func (o *DockerfileBuildOptions) Validate() error {
	if o == nil {
		return nil
	}
	if len(o.Args) > DockerfileBuildArgsMax {
		return fmt.Errorf("build.args has %d arguments; the limit is %d", len(o.Args), DockerfileBuildArgsMax)
	}
	for _, name := range o.ArgNames() {
		value := o.Args[name]
		if !dockerfileBuildArgName.MatchString(name) {
			return fmt.Errorf("build.args name %q must be letters, digits and underscores, not starting with a digit", name)
		}
		if len(value) > DockerfileBuildArgValueMaxBytes {
			return fmt.Errorf("build.args.%s is %d bytes; the limit is %d", name, len(value), DockerfileBuildArgValueMaxBytes)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("build.args.%s must not contain NUL", name)
		}
	}
	if encoded, _ := json.Marshal(o.Args); len(encoded) > DockerfileBuildArgsEncodedMaxBytes {
		return fmt.Errorf("build.args encode to %d bytes; the limit is %d", len(encoded), DockerfileBuildArgsEncodedMaxBytes)
	}
	if o.Target != "" && (len(o.Target) > DockerfileBuildTargetMaxBytes || !dockerfileBuildStageName.MatchString(o.Target)) {
		return fmt.Errorf("build.target %q is not a valid stage name", o.Target)
	}
	return nil
}

// SHA256 binds the builder guest's acknowledgement and the artifact cache to
// the same values. Empty options hash to "", so legacy builds are unchanged.
func (o *DockerfileBuildOptions) SHA256() string {
	if o.IsZero() {
		return ""
	}
	raw, _ := json.Marshal(o)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
