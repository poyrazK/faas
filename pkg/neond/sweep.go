package neond

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// The key sweep (ADR-20261112160000 decision 3): neond keeps project keys in
// step with the organization's projects without any caller. Each pass
//
//   - mints a key for every project that has none (projects created before
//     project keys, or whose mint failed),
//   - revokes the keys of projects Neon no longer lists, and
//   - rotates keys older than the maximum age,
//
// at most SweepConfig.Budget Neon key calls per pass, spaced Pace apart
// (Neon admits 10 key creations a second).

// SweepConfig paces the sweep.
type SweepConfig struct {
	// Interval is the time between passes.
	Interval time.Duration
	// MaxAge rotates a key once it is this old.
	MaxAge time.Duration
	// Budget bounds the mints, rotations and revocations of one pass.
	Budget int
	// Pace is the pause between two of them.
	Pace time.Duration
}

// DefaultSweepConfig is neond's sweep unless configured otherwise.
var DefaultSweepConfig = SweepConfig{Interval: 10 * time.Minute, MaxAge: 90 * 24 * time.Hour, Budget: 50, Pace: 250 * time.Millisecond}

// maxProjectPages bounds one listing of the organization's projects
// (400 a page).
const maxProjectPages = 25

// errIncompleteListing stops a pass that could not see every project: it
// would otherwise revoke keys of projects it merely missed.
var errIncompleteListing = errors.New("neond: project listing incomplete")

// SweepResult is what one pass did.
type SweepResult struct {
	Projects, WithoutKey, Minted, Rotated, Revoked, Failed int
}

// RunKeySweep runs passes until ctx ends. It does nothing without project
// keys.
func (s *Server) RunKeySweep(ctx context.Context, cfg SweepConfig) {
	if s.keys == nil {
		return
	}
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		result, err := s.SweepKeys(ctx, cfg, time.Now())
		if err != nil {
			s.log.Warn("neond: key sweep stopped early", "err", err)
		}
		s.log.Info("neond: key sweep", "projects", result.Projects, "without_key", result.WithoutKey,
			"minted", result.Minted, "rotated", result.Rotated, "revoked", result.Revoked, "failed", result.Failed)
		timer.Reset(cfg.Interval)
	}
}

// SweepKeys runs one pass at now.
func (s *Server) SweepKeys(ctx context.Context, cfg SweepConfig, now time.Time) (SweepResult, error) {
	var result SweepResult
	if s.keys == nil {
		return result, nil
	}
	projects, err := s.listProjects(ctx)
	if err != nil {
		return result, err
	}
	keys, err := s.keys.Store.LiveProjectKeys(ctx)
	if err != nil {
		return result, err
	}
	result.Projects = len(projects)
	keyed := make(map[string]LiveKey, len(keys))
	for _, key := range keys {
		keyed[key.Project] = key
	}
	budget := cfg.Budget
	spend := func() bool {
		if budget <= 0 {
			return false
		}
		if budget < cfg.Budget && cfg.Pace > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(cfg.Pace):
			}
		}
		budget--
		return true
	}
	record := func(op string, err error, attrs ...any) {
		if err != nil {
			result.Failed++
			s.keyOps.WithLabelValues(op, "error").Inc()
			s.log.Warn("neond: key sweep "+op+" failed", append(attrs, "err", err)...)
			return
		}
		s.keyOps.WithLabelValues(op, "ok").Inc()
	}

	// Backfill.
	for _, project := range projects {
		if _, ok := keyed[project]; ok {
			continue
		}
		result.WithoutKey++
		if !spend() {
			continue
		}
		err := s.mintProjectKey(ctx, project)
		if err == nil {
			result.Minted++
			result.WithoutKey--
		}
		record("backfill", err, "project", project)
	}
	// Orphans: keys of projects Neon no longer lists.
	listed := make(map[string]bool, len(projects))
	for _, project := range projects {
		listed[project] = true
	}
	for _, key := range keys {
		if listed[key.Project] || !spend() {
			continue
		}
		err := s.revokeKey(ctx, key.KeyID)
		if err == nil {
			s.keys.forget(key.Project)
			err = s.keys.Store.MarkProjectKeyRevoked(ctx, key.KeyID)
		}
		if err == nil {
			result.Revoked++
		}
		record("orphan_revoke", err, "project", key.Project, "key_id", key.KeyID)
	}
	// Rotation.
	for _, key := range keys {
		if !listed[key.Project] || now.Sub(key.CreatedAt) < cfg.MaxAge || !spend() {
			continue
		}
		err := s.rotateProjectKey(ctx, key)
		if err == nil {
			result.Rotated++
		}
		record("rotate", err, "project", key.Project, "key_id", key.KeyID)
	}
	s.withoutKey.Set(float64(result.WithoutKey))
	return result, ctx.Err()
}

// rotateProjectKey replaces a project's key: mint the new key, revoke the old
// one at Neon, then swap the rows. A failure before the swap revokes the new
// key; a failed swap leaves the old row naming a revoked key, which the next
// pass rotates again (its revocation then finds nothing to revoke).
func (s *Server) rotateProjectKey(ctx context.Context, old LiveKey) error {
	minted, err := s.mintAtNeon(ctx, old.Project)
	if err != nil {
		return err
	}
	sealed, err := s.seal(minted.Key)
	if err == nil {
		err = s.revokeKey(ctx, old.KeyID)
	}
	if err == nil {
		s.keys.forget(old.Project)
		err = s.keys.Store.ReplaceProjectKey(ctx, old.Project, old.KeyID, minted.ID, sealed)
	}
	if err != nil {
		if revokeErr := s.revokeKey(ctx, minted.ID); revokeErr != nil {
			return errors.Join(err, fmt.Errorf("neond: revoke unstored key %d: %w", minted.ID, revokeErr))
		}
		return err
	}
	s.keys.forget(old.Project)
	return nil
}

// listProjects lists every project of the organization with the admin key.
func (s *Server) listProjects(ctx context.Context) ([]string, error) {
	var projects []string
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < maxProjectPages; page++ {
		query := url.Values{"limit": {"400"}, "org_id": {s.keys.Org}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		code, body, _, err := s.call(ctx, s.adminKey, http.MethodGet, "/projects", query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var response struct {
			Projects []struct {
				ID string `json:"id"`
			} `json:"projects"`
			UnavailableProjectIDs []string `json:"unavailable_project_ids"`
			Pagination            struct {
				Cursor string `json:"cursor"`
			} `json:"pagination"`
		}
		if code != http.StatusOK || len(body) > MaxResponseBytes || json.Unmarshal(body, &response) != nil ||
			response.Projects == nil || len(response.UnavailableProjectIDs) > 0 {
			return nil, fmt.Errorf("%w: page %d, Neon status %d", errIncompleteListing, page, code)
		}
		for _, project := range response.Projects {
			if !validID.MatchString(project.ID) {
				return nil, fmt.Errorf("%w: malformed project ID", errIncompleteListing)
			}
			if !seen[project.ID] {
				seen[project.ID] = true
				projects = append(projects, project.ID)
			}
		}
		if len(response.Projects) == 0 || response.Pagination.Cursor == "" {
			sort.Strings(projects)
			return projects, nil
		}
		if response.Pagination.Cursor == cursor {
			return nil, fmt.Errorf("%w: repeated cursor", errIncompleteListing)
		}
		cursor = response.Pagination.Cursor
	}
	return nil, fmt.Errorf("%w: more than %d pages", errIncompleteListing, maxProjectPages)
}
