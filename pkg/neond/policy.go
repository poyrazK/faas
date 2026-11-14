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

// ErrSubjectRateLimited is a destructive call past one account's hourly
// limit. It is an ErrRateLimited.
var ErrSubjectRateLimited = fmt.Errorf("%w for one account", ErrRateLimited)

// LiveResources reads apid's managed PostgreSQL rows for a Neon project.
// A provider resource is the project ID itself for a database on the
// project's default branch, "<project>/<branch>" for one on a branch.
type LiveResources interface {
	// LiveResourcesInProject lists the provider resources of the live
	// databases in the project.
	LiveResourcesInProject(ctx context.Context, project string) ([]string, error)
	// AccountsInProject lists the accounts whose databases, in any state,
	// name the project.
	AccountsInProject(ctx context.Context, project string) ([]string, error)
}

// Limits bound destructive calls per hour: fleet-wide per neond, and per
// subject, the account whose databases name the project.
type Limits struct {
	PerHour, PerSubjectPerHour int
}

// BranchReader reports whether a branch is its project's default branch.
type BranchReader func(ctx context.Context, project, branch string) (isDefault bool, err error)

// legacyRole is the API role shape earlier releases created per credential
// identity; only these may be deleted. The owner role never is.
var legacyRole = regexp.MustCompile(`^gregale_[0-9a-f]{40}$`)

// Policy decides destructive calls and writes to a customer's project
// (ADR-20261112160000 decision 2.3).
type Policy struct {
	live    LiveResources
	branch  BranchReader
	limiter *Limiter
}

// NewPolicy builds the policy.
func NewPolicy(live LiveResources, branch BranchReader, limits Limits, now func() time.Time) *Policy {
	return &Policy{live: live, branch: branch, limiter: NewLimiter(limits.PerHour, limits.PerSubjectPerHour, time.Hour, now)}
}

// namedOnly are the routes allowed only on a project a database row names:
// quota writes (the project's settings) and role deletes.
var namedOnly = map[string]bool{"update project": true, "delete role": true}

// Check returns nil when the call may be forwarded. A read or ordinary write
// always may, except a quota write, which needs a database row naming the
// project. A destructive call must leave every live database's project and
// branch in place and fit both rate limits.
func (p *Policy) Check(ctx context.Context, route Route, params Params, path string) error {
	if route.Kind != KindDestructive && !namedOnly[route.Name] {
		return nil
	}
	if route.Name == "delete role" {
		role := path[strings.LastIndex(path, "/")+1:]
		if !legacyRole.MatchString(role) {
			return fmt.Errorf("%w: only legacy credential roles are deleted", ErrRefused)
		}
	}
	accounts, err := p.live.AccountsInProject(ctx, params.Project)
	if err != nil {
		return err
	}
	if namedOnly[route.Name] && len(accounts) == 0 {
		return fmt.Errorf("%w: no database names project %s", ErrRefused, params.Project)
	}
	if route.Kind != KindDestructive {
		return nil
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
	if subject, ok := p.limiter.Allow(accounts); !ok {
		if subject != "" {
			return fmt.Errorf("%w: account %s", ErrSubjectRateLimited, subject)
		}
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

// Limiter admits at most limit events per window fleet-wide and perSubject
// per subject, counting the events of the last window.
type Limiter struct {
	mu         sync.Mutex
	limit      int
	perSubject int
	window     time.Duration
	now        func() time.Time
	events     []time.Time
	subjects   map[string][]time.Time
}

func NewLimiter(limit, perSubject int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{limit: limit, perSubject: perSubject, window: window, now: now, subjects: map[string][]time.Time{}}
}

// maxSubjects bounds the subjects the limiter tracks; past it, subjects
// without an event in the window are dropped.
const maxSubjects = 4096

// Allow records an event against the fleet and each subject and reports
// whether it fits every limit. A refused event is recorded nowhere; the
// subject it names is the one over its limit, or "" for the fleet limit.
func (l *Limiter) Allow(subjects []string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.events = l.trim(l.events, now)
	if len(l.events) >= l.limit {
		return "", false
	}
	for _, subject := range subjects {
		events := l.trim(l.subjects[subject], now)
		l.subjects[subject] = events
		if len(events) >= l.perSubject {
			return subject, false
		}
	}
	l.events = append(l.events, now)
	for _, subject := range subjects {
		l.subjects[subject] = append(l.subjects[subject], now)
	}
	if len(l.subjects) > maxSubjects {
		for subject, events := range l.subjects {
			if len(l.trim(events, now)) == 0 {
				delete(l.subjects, subject)
			}
		}
	}
	return "", true
}

// trim drops the events older than the window.
func (l *Limiter) trim(events []time.Time, now time.Time) []time.Time {
	cut := 0
	for cut < len(events) && now.Sub(events[cut]) >= l.window {
		cut++
	}
	return events[cut:]
}
