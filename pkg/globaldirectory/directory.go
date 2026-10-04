// Package globaldirectory owns the durable global-to-regional app directory
// contract (ADR-476). Only apid mutates intent; ADR-482 retains scoped ciphertext.
package globaldirectory

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
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
	AccountID     string
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

func ValidSlug(slug string) bool {
	if len(slug) < api.GlobalDirectorySlugMinBytes || len(slug) > api.GlobalDirectorySlugMaxBytes || slug[0] == '-' || slug[len(slug)-1] == '-' {
		return false
	}
	for _, ch := range slug {
		if ch != '-' && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
			return false
		}
	}
	return !api.IsReservedAppSlug(slug)
}

// API origins are operator-owned HTTPS origins, without paths or credentials.
// Canonical spelling makes generation changes and SDK resolution unambiguous.
func ValidAPIBase(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.String() != origin ||
		len(origin) > api.GlobalDirectoryAPIOriginMaxBytes || strings.ToLower(u.Host) != u.Host {
		return false
	}
	for _, ch := range u.Host {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && !strings.ContainsRune(".:-[]", ch) {
			return false
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > api.GlobalDirectoryOriginPortMax || strconv.Itoa(n) != port {
			return false
		}
	}
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") {
		ip, err := netip.ParseAddr(host)
		return err == nil && ip.Is6() && ip.String() == host
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Is4() && ip.String() == host
	}
	if len(host) > api.GlobalDirectoryOriginHostMaxBytes || strings.ContainsAny(host, ":[]") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > api.GlobalDirectoryOriginLabelMaxBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	return true
}

func (c Cell) Validate() error {
	if !ValidID(c.ID) || c.Region == "" || !api.ValidComputeRegion(c.Region) || !ValidAPIBase(c.APIBase) {
		return fmt.Errorf("%w: canonical cell UUID, region and HTTPS API origin required", ErrConflict)
	}
	return nil
}

func (b Binding) Validate() error {
	if !ValidID(b.CellID) || b.Region == "" || !api.ValidComputeRegion(b.Region) || b.CellGeneration < 1 ||
		!ValidID(b.RegionalAccountID) || !ValidID(b.RegionalAppID) || !ValidSlug(b.RegionalAppSlug) {
		return fmt.Errorf("%w: incomplete regional binding", ErrConflict)
	}
	return nil
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
