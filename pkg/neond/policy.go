package neond

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrRefused is a call the policy refuses: it would destroy what a live
// database runs on.
var ErrRefused = errors.New("neond: refused by policy")

// ErrRateLimited is a destructive call past the hourly limit.
var ErrRateLimited = errors.New("neond: destructive call rate limit reached")

// LiveResources lists the provider resources of live managed PostgreSQL
// databases in a Neon project: the project ID itself for a database on the
// project's default branch, "<project>/<branch>" for one on a branch.
type LiveResources interface {
	LiveResourcesInProject(ctx context.Context, project string) ([]string, error)
}

// BranchReader reports whether a branch is its project's default branch.
type BranchReader func(ctx context.Context, project, branch string) (isDefault bool, err error)

// legacyRole is the API role shape earlier releases created per credential
// identity; only these may be deleted. The owner role never is.
var legacyRole = regexp.MustCompile(`^gregale_[0-9a-f]{40}$`)

// Policy decides destructive calls (ADR-20261112160000 decision 2.3).
type Policy struct {
	live    LiveResources
	branch  BranchReader
	limiter *Limiter
}

// NewPolicy builds the policy. limit is how many destructive calls neond
// forwards per hour.
func NewPolicy(live LiveResources, branch BranchReader, limit int, now func() time.Time) *Policy {
	return &Policy{live: live, branch: branch, limiter: NewLimiter(limit, time.Hour, now)}
}

// Check returns nil when the call may be forwarded. A read or ordinary write
// always may; a destructive call must leave every live database's project
// and branch in place and fit the rate limit.
func (p *Policy) Check(ctx context.Context, route Route, params Params, path string) error {
	if route.Kind != KindDestructive {
		return nil
	}
	if route.Name == "delete role" {
		role := path[strings.LastIndex(path, "/")+1:]
		if !legacyRole.MatchString(role) {
			return fmt.Errorf("%w: only legacy credential roles are deleted", ErrRefused)
		}
	}
	if route.Name == "delete project" || route.Name == "delete branch" {
		live, err := p.live.LiveResourcesInProject(ctx, params.Project)
		if err != nil {
			return err
		}
		if err := p.checkLive(ctx, route, params, live); err != nil {
			return err
		}
	}
	if !p.limiter.Allow() {
		return ErrRateLimited
	}
	return nil
}

func (p *Policy) checkLive(ctx context.Context, route Route, params Params, live []string) error {
	if len(live) == 0 {
		return nil
	}
	if route.Name == "delete project" {
		return fmt.Errorf("%w: project %s holds %d live database(s)", ErrRefused, params.Project, len(live))
	}
	onDefault := false
	for _, resource := range live {
		if resource == params.Project+"/"+params.Branch {
			return fmt.Errorf("%w: branch %s holds a live database", ErrRefused, params.Branch)
		}
		onDefault = onDefault || resource == params.Project
	}
	if !onDefault {
		return nil
	}
	isDefault, err := p.branch(ctx, params.Project, params.Branch)
	if err != nil {
		return err
	}
	if isDefault {
		return fmt.Errorf("%w: branch %s is the default branch of a live database's project", ErrRefused, params.Branch)
	}
	return nil
}

// Limiter admits at most limit events per window, counting the events of the
// last window.
type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	events []time.Time
}

func NewLimiter(limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{limit: limit, window: window, now: now}
}

// Allow records an event and reports whether it fits the limit.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cut := 0
	for cut < len(l.events) && now.Sub(l.events[cut]) >= l.window {
		cut++
	}
	l.events = l.events[cut:]
	if len(l.events) >= l.limit {
		return false
	}
	l.events = append(l.events, now)
	return true
}
