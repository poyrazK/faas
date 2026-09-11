package githubd

import (
	"context"
	"fmt"
	"strings"
)

// InstallationLifecycleStore is the persistence seam for GitHub App access
// changes. Implementations must make each operation idempotent: GitHub retries
// lifecycle deliveries just like push deliveries.
type InstallationLifecycleStore interface {
	RevokeGitHubInstallation(ctx context.Context, installationID int64) error
	RemoveGitHubRepositories(ctx context.Context, installationID int64, repoFullNames []string) error
	RenameGitHubRepository(ctx context.Context, installationID int64, oldRepoFullName, newRepoFullName string) error
}

// HandleInstallationLifecycle processes GitHub App installation and repository
// access changes. Unknown actions are acknowledged deliberately; GitHub emits
// actions unrelated to deployment access and retrying those forever would fill
// the durable inbox without changing local state.
func (s *Service) HandleInstallationLifecycle(ctx context.Context, eventType string, body []byte) error {
	switch eventType {
	case "installation":
		ev, err := DecodeInstallation(body)
		if err != nil {
			return err
		}
		if s.Lifecycle == nil {
			s.Log.Warn("githubd: lifecycle event received without lifecycle store", "event_type", eventType)
			return nil
		}
		switch strings.ToLower(ev.Action) {
		case "deleted", "suspend", "suspended":
			if err := s.Lifecycle.RevokeGitHubInstallation(ctx, ev.Installation.ID); err != nil {
				return fmt.Errorf("githubd: revoke installation %d: %w", ev.Installation.ID, err)
			}
			if s.InvalidateInstallation != nil {
				s.InvalidateInstallation(ev.Installation.ID)
			}
			if s.InvalidateInstallationSecrets != nil {
				s.InvalidateInstallationSecrets(ev.Installation.ID)
			}
			s.Log.Info("githubd: revoked GitHub installation", "installation_id", ev.Installation.ID, "action", ev.Action, "sender", ev.Sender.Login)
		default:
			s.Log.Debug("githubd: acknowledged installation action", "installation_id", ev.Installation.ID, "action", ev.Action)
		}
		return nil

	case "installation_repositories":
		ev, err := DecodeInstallationRepositories(body)
		if err != nil {
			return err
		}
		if s.Lifecycle == nil {
			s.Log.Warn("githubd: lifecycle event received without lifecycle store", "event_type", eventType)
			return nil
		}
		if strings.EqualFold(ev.Action, "removed") {
			repos := repositoryFullNames(ev.RepositoriesRemoved)
			if len(repos) > 0 {
				if err := s.Lifecycle.RemoveGitHubRepositories(ctx, ev.Installation.ID, repos); err != nil {
					return fmt.Errorf("githubd: remove repositories from installation %d: %w", ev.Installation.ID, err)
				}
				if s.InvalidateInstallationBindings != nil {
					s.InvalidateInstallationBindings(ev.Installation.ID)
				}
			}
		}
		return nil

	case "repository":
		ev, err := DecodeRepository(body)
		if err != nil {
			return err
		}
		if s.Lifecycle == nil {
			s.Log.Warn("githubd: lifecycle event received without lifecycle store", "event_type", eventType)
			return nil
		}
		switch strings.ToLower(ev.Action) {
		case "deleted", "archived":
			if err := s.Lifecycle.RemoveGitHubRepositories(ctx, ev.Installation.ID, []string{ev.Repository.FullName}); err != nil {
				return fmt.Errorf("githubd: detach repository %q: %w", ev.Repository.FullName, err)
			}
			if s.InvalidateInstallationBindings != nil {
				s.InvalidateInstallationBindings(ev.Installation.ID)
			}
		case "renamed", "transferred":
			oldName := ev.Changes.Repository.Name.From
			if strings.EqualFold(ev.Action, "transferred") && ev.Changes.Owner.From.Login != "" {
				oldName = ev.Changes.Owner.From.Login + "/" + ev.Repository.Name
			}
			if oldName == "" {
				// GitHub normally includes changes.repository.name.from;
				// acknowledge a partial retry without guessing an owner.
				s.Log.Warn("githubd: repository rename missing old name", "installation_id", ev.Installation.ID, "repo", ev.Repository.FullName)
				return nil
			}
			if !strings.Contains(oldName, "/") {
				if ev.Repository.Owner.Login == "" {
					return fmt.Errorf("githubd: repository rename missing owner.login for old repository %q", oldName)
				}
				oldName = ev.Repository.Owner.Login + "/" + oldName
			}
			if err := s.Lifecycle.RenameGitHubRepository(ctx, ev.Installation.ID, oldName, ev.Repository.FullName); err != nil {
				return fmt.Errorf("githubd: rename repository %q to %q: %w", oldName, ev.Repository.FullName, err)
			}
			if s.InvalidateInstallationBindings != nil {
				s.InvalidateInstallationBindings(ev.Installation.ID)
			}
		}
		return nil
	default:
		return fmt.Errorf("githubd: unsupported lifecycle event %q", eventType)
	}
}

func repositoryFullNames(repos []PushRepository) []string {
	out := make([]string, 0, len(repos))
	seen := make(map[string]struct{}, len(repos))
	for _, repo := range repos {
		if repo.FullName == "" {
			continue
		}
		if _, ok := seen[repo.FullName]; ok {
			continue
		}
		seen[repo.FullName] = struct{}{}
		out = append(out, repo.FullName)
	}
	return out
}
