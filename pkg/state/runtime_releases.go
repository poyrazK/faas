package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeRelease identifies published drive0 bytes, independently of source wishes,
// builder images and the host kernel. Publication is not upgrade qualification.
type RuntimeRelease struct {
	ID              string
	Runtime         string
	Architecture    string
	SourceRef       string
	GuestInitSHA256 string
	LayoutVersion   string
	BaseSHA256      string
	CreatedAt       time.Time
}

func (r RuntimeRelease) BaseKey() string {
	return "base/releases/runner-" + r.Runtime + "-" + r.Architecture + "-" + r.ID + ".ext4"
}
func (r RuntimeRelease) Identity() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{r.Runtime, r.Architecture, r.SourceRef, r.GuestInitSHA256, r.LayoutVersion, r.BaseSHA256}, "\n")))
	return hex.EncodeToString(sum[:])
}

var runtimeSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)
var runtimeRef = regexp.MustCompile(`^[^\s@]+@sha256:[a-f0-9]{64}$`)

func (r RuntimeRelease) Validate() error {
	switch r.Runtime {
	case "node22", "node24", "python312", "python313", "go124", "go124-alpine":
	default:
		return ErrInvalidArgument
	}
	if (r.Architecture != "amd64" && r.Architecture != "arm64") || !runtimeRef.MatchString(r.SourceRef) || !runtimeSHA.MatchString(r.GuestInitSHA256) || !runtimeSHA.MatchString(r.BaseSHA256) || len(r.LayoutVersion) < 1 || len(r.LayoutVersion) > api.RuntimeReleaseLayoutMaxBytes || r.ID != r.Identity() {
		return ErrInvalidArgument
	}
	return nil
}

// RuntimeReleaseStore binds physical artifacts, so clones and promotions that
// reuse a layer retain its runtime. Readers never substitute the newest release.
type RuntimeReleaseStore interface {
	PublishRuntimeRelease(context.Context, RuntimeRelease) (RuntimeRelease, error)
	RuntimeReleaseByID(context.Context, string) (RuntimeRelease, error)
	FindRuntimeRelease(context.Context, RuntimeRelease) (RuntimeRelease, error)
	ListRuntimeReleases(context.Context, string, string) ([]RuntimeRelease, error)
	BindDeploymentRuntimeRelease(context.Context, string, string, string) error
	RuntimeReleaseForArtifact(context.Context, string, string) (RuntimeRelease, error)
	BuildRuntimeBaseRef(context.Context, string) (string, error)
}

func sameRuntimeInputs(a, b RuntimeRelease) bool {
	return a.Runtime == b.Runtime && a.Architecture == b.Architecture && a.SourceRef == b.SourceRef && a.GuestInitSHA256 == b.GuestInitSHA256 && a.LayoutVersion == b.LayoutVersion
}
func runtimeArtifactBindingKey(account, key string) string {
	return fmt.Sprintf("%s\x00%s", account, key)
}

var _ RuntimeReleaseStore = (*MemStore)(nil)

func (m *MemStore) PublishRuntimeRelease(_ context.Context, r RuntimeRelease) (RuntimeRelease, error) {
	if err := r.Validate(); err != nil {
		return RuntimeRelease{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.runtimeReleases {
		if sameRuntimeInputs(existing, r) {
			return existing, nil
		}
	}
	if m.runtimeReleases == nil {
		m.runtimeReleases = make(map[string]RuntimeRelease)
	}
	r.CreatedAt = time.Now().UTC()
	m.runtimeReleases[r.ID] = r
	return r, nil
}
func (m *MemStore) RuntimeReleaseByID(_ context.Context, id string) (RuntimeRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runtimeReleases[id]
	if !ok {
		return r, ErrNotFound
	}
	return r, nil
}
func (m *MemStore) FindRuntimeRelease(_ context.Context, r RuntimeRelease) (RuntimeRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.runtimeReleases {
		if sameRuntimeInputs(existing, r) {
			return existing, nil
		}
	}
	return RuntimeRelease{}, ErrNotFound
}
func (m *MemStore) BindDeploymentRuntimeRelease(_ context.Context, depID, key, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[depID]
	if !ok || d.RootfsKey != key || (d.Status != DeployPending && d.Status != DeployBuilding && d.Status != DeployImaging) {
		return ErrConflict
	}
	if _, ok := m.runtimeReleases[id]; !ok {
		return ErrNotFound
	}
	app, ok := m.apps[d.AppID]
	if !ok || key == "" || len(key) > api.RuntimeReleaseArtifactKeyMaxBytes {
		return ErrInvalidArgument
	}
	k := runtimeArtifactBindingKey(app.AccountID, key)
	if old, ok := m.runtimeArtifactBindings[k]; ok && old != id {
		return ErrConflict
	}
	if m.runtimeArtifactBindings == nil {
		m.runtimeArtifactBindings = make(map[string]string)
	}
	m.runtimeArtifactBindings[k] = id
	return nil
}
func (m *MemStore) RuntimeReleaseForArtifact(_ context.Context, account, key string) (RuntimeRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.runtimeArtifactBindings[runtimeArtifactBindingKey(account, key)]
	if !ok {
		return RuntimeRelease{}, ErrNotFound
	}
	return m.runtimeReleases[id], nil
}
func (m *MemStore) BuildRuntimeBaseRef(_ context.Context, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.buildProvenance[id]
	if !ok {
		return "", ErrNotFound
	}
	return p.RuntimeBaseRef, nil
}
func runtimeCatalogLimit() int { return api.RuntimeReleaseCatalogLimit }
