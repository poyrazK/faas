// Package globaldirectory owns the durable global-to-regional app directory
// contract (ADR-476). Only apid mutates intent; ADR-482 retains scoped ciphertext.
package globaldirectory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrNotFound  = errors.New("global directory identity not found")
	ErrConflict  = errors.New("global directory identity conflicts")
	ErrLimit     = errors.New("global app identity limit reached")
	ErrStaleCell = errors.New("global directory cell requires verification")
	ErrBoundCell = errors.New("regional databases cannot own the global directory")
)

const AllSelectedRegions = "all_selected_regions"

type Cell struct {
	ID         string
	Region     string
	APIBase    string
	Enabled    bool
	Generation int64
}

type App struct {
	ID            string
	OwnerID       string
	Slug          string
	RoutingPolicy string
	CreatedAt     time.Time
}

// Binding is a verified regional response and the exact registry generation
// that was contacted. It deliberately has no credential field.
type Binding struct {
	CellID            string
	Region            string
	CellGeneration    int64
	RegionalAccountID string
	RegionalOrgID     string `json:",omitempty"` // Preserve ADR-482 personal ciphertext spelling.
	RegionalAppID     string
	RegionalAppSlug   string
}

type Target struct {
	Binding
	GlobalAppID        string
	VerifiedAt         time.Time
	APIBase            string
	CellEnabled        bool
	RegistryGeneration int64
}

func (t Target) Resolvable() bool {
	return t.CellEnabled && t.CellGeneration == t.RegistryGeneration
}

type Store interface {
	ReleaseStore
	CheckScope(context.Context) error
	PutCell(context.Context, Cell) (Cell, error)
	ListCells(context.Context) ([]Cell, error)
	CellByRegion(context.Context, string) (Cell, error)
	CreateApp(context.Context, string, string, int) (App, error)
	AppBySlug(context.Context, string, string) (App, error)
	ListApps(context.Context, string) ([]App, error)
	AttachTarget(context.Context, string, string, Binding) (Target, error)
	ListTargets(context.Context, string, string) ([]Target, error)
	DetachTarget(context.Context, string, string, string) error
}

func ValidID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

// Owners come from the local state store, whose portable memory adapter uses
// compact UUID spelling. Wire identities still require canonical UUIDs.
func validOwnerID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil
}

func ValidSlug(slug string) bool { return api.ValidGlobalAppSlug(slug) }

func ValidAPIBase(origin string) bool { return api.ValidGlobalDirectoryAPIBase(origin) }

func (c Cell) Validate() error {
	if !ValidID(c.ID) || c.Region == "" || !api.ValidComputeRegion(c.Region) || !ValidAPIBase(c.APIBase) {
		return fmt.Errorf("%w: canonical cell UUID, region and HTTPS API origin required", ErrConflict)
	}
	return nil
}

func (b Binding) Validate() error {
	if !ValidID(b.CellID) || b.Region == "" || !api.ValidComputeRegion(b.Region) || b.CellGeneration < 1 ||
		!ValidID(b.RegionalAccountID) || (b.RegionalOrgID != "" && !ValidID(b.RegionalOrgID)) || !ValidID(b.RegionalAppID) || !ValidSlug(b.RegionalAppSlug) {
		return fmt.Errorf("%w: incomplete regional binding", ErrConflict)
	}
	return nil
}

func (b Binding) regionalOwner() string {
	if b.RegionalOrgID != "" {
		return "organization/" + b.RegionalOrgID
	}
	return "account/" + b.RegionalAccountID
}

func Resolve(ctx context.Context, store Store, accountID, slug, region string) (Target, error) {
	if region == "" || !api.ValidComputeRegion(region) {
		return Target{}, fmt.Errorf("%w: explicit canonical region required", ErrNotFound)
	}
	targets, err := store.ListTargets(ctx, accountID, slug)
	if err != nil {
		return Target{}, err
	}
	for _, target := range targets {
		if target.Region == region {
			if !target.Resolvable() {
				return Target{}, ErrStaleCell
			}
			return target, nil
		}
	}
	return Target{}, ErrNotFound
}
