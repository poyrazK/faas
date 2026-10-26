package gregalemanifest

import (
	"fmt"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// BuildConfig is the `build:` section of gregale.yaml: the Dockerfile build
// options a Compose service's build: takes, for a single-app source deploy
// (ADR-20261026152731). Declaring it makes the deploy a Dockerfile build.
//
//	build:
//	  dockerfile: docker/Dockerfile.prod   # relative to the source root
//	  target: runtime
//	  args: {NODE_ENV: production}
//	  secrets:
//	    - {id: npmrc, secret: NPM_TOKEN}   # RUN --mount=type=secret,id=npmrc
type BuildConfig struct {
	Dockerfile string            `yaml:"dockerfile,omitempty" toml:"dockerfile"`
	Target     string            `yaml:"target,omitempty" toml:"target"`
	Args       map[string]string `yaml:"args,omitempty" toml:"args"`
	Secrets    []BuildSecret     `yaml:"secrets,omitempty" toml:"secrets"`
}

// BuildSecret mounts the app secret Secret as the BuildKit secret ID.
type BuildSecret struct {
	ID     string `yaml:"id" toml:"id"`
	Secret string `yaml:"secret" toml:"secret"`
}

// DockerfilePath returns the selected Dockerfile, relative to the source
// root; "Dockerfile" when the section does not name one.
func (b *BuildConfig) DockerfilePath() string {
	if b == nil || b.Dockerfile == "" {
		return "Dockerfile"
	}
	return path.Clean(strings.TrimPrefix(b.Dockerfile, "./"))
}

// Options returns the build arguments, target and secrets, or nil when the
// section sets none.
func (b *BuildConfig) Options() *api.DockerfileBuildOptions {
	if b == nil {
		return nil
	}
	opts := &api.DockerfileBuildOptions{Args: b.Args, Target: b.Target}
	for _, s := range b.Secrets {
		opts.Secrets = append(opts.Secrets, api.DockerfileBuildSecret{ID: s.ID, Secret: s.Secret})
	}
	if opts.IsZero() {
		return nil
	}
	return opts
}

// Validate checks the Dockerfile path stays inside the source root and the
// options against the Compose limits in pkg/api/limits.go.
func (b *BuildConfig) Validate() error {
	if b == nil {
		return nil
	}
	if b.Dockerfile != "" {
		p := b.DockerfilePath()
		if path.IsAbs(b.Dockerfile) || p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.ContainsAny(p, "\\\x00") {
			return fmt.Errorf("dockerfile %q must be a relative path inside the source root", b.Dockerfile)
		}
	}
	return b.Options().Validate()
}
