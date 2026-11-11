package agents

// Sandbox templates (ADR-20261027090013): a named Dockerfile build an account
// starts sessions from. Each template owns one platform-managed app
// (apps.sandbox_template) whose deployments are its builds; apid is the only
// writer of both.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// ErrTemplateExists is a create for a name the account already uses.
var ErrTemplateExists = errors.New("agents: sandbox template already exists")

// SandboxTemplateSpec is what a template runs at session boot.
type SandboxTemplateSpec struct {
	// StartCmd runs in the background when a session boots; ReadyCmd is
	// polled until it exits 0, for at most ReadyTimeoutSec, before the
	// session is ready. Both are optional.
	StartCmd        string
	ReadyCmd        string
	ReadyTimeoutSec int
}

// SandboxTemplate is a template with its build state.
type SandboxTemplate struct {
	ID        string
	AccountID string
	Name      string
	AppID     string
	AppSlug   string
	SandboxTemplateSpec
	CreatedAt time.Time
	UpdatedAt time.Time
	// BuildID, BuildStatus and BuildError describe the newest build (a
	// deployment of the template's app); empty before the first one.
	BuildID     string
	BuildStatus string
	BuildError  string
	// LiveDeploymentID and LiveRootfsKey are the newest finished build,
	// what a session boots; empty until one finishes.
	LiveDeploymentID string
	LiveRootfsKey    string
	LiveBuiltAt      *time.Time
}

// Ready reports whether sessions can start from the template.
func (t SandboxTemplate) Ready() bool { return t.LiveRootfsKey != "" }

// CreateSandboxTemplate records a template over its build app.
func (s *Store) CreateSandboxTemplate(ctx context.Context, accountID, name, appID string, spec SandboxTemplateSpec) (string, error) {
	id, err := s.q.InsertSandboxTemplate(ctx, s.pool, sqlc.InsertSandboxTemplateParams{AccountID: accountID, Name: name, AppID: appID,
		StartCmd: optText(spec.StartCmd), ReadyCmd: optText(spec.ReadyCmd), ReadyTimeoutS: int32(spec.ReadyTimeoutSec)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTemplateExists
	}
	if err != nil {
		return "", fmt.Errorf("insert sandbox template: %w", err)
	}
	return id, nil
}

// UpdateSandboxTemplateSpec replaces a template's start and ready commands.
func (s *Store) UpdateSandboxTemplateSpec(ctx context.Context, accountID, name string, spec SandboxTemplateSpec) error {
	n, err := s.q.UpdateSandboxTemplateCommands(ctx, s.pool, sqlc.UpdateSandboxTemplateCommandsParams{AccountID: accountID, Name: name,
		StartCmd: optText(spec.StartCmd), ReadyCmd: optText(spec.ReadyCmd), ReadyTimeoutS: int32(spec.ReadyTimeoutSec)})
	if err != nil {
		return fmt.Errorf("update sandbox template: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSandboxTemplate removes a template and returns its build app, which
// the caller deletes.
func (s *Store) DeleteSandboxTemplate(ctx context.Context, accountID, name string) (string, error) {
	appID, err := s.q.DeleteSandboxTemplate(ctx, s.pool, sqlc.DeleteSandboxTemplateParams{AccountID: accountID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("delete sandbox template: %w", err)
	}
	return appID, nil
}

// SandboxTemplates lists an account's templates by name.
func (s *Store) SandboxTemplates(ctx context.Context, accountID string) ([]SandboxTemplate, error) {
	return s.sandboxTemplates(ctx, accountID, "")
}

// SandboxTemplate returns one of the account's templates.
func (s *Store) SandboxTemplate(ctx context.Context, accountID, name string) (SandboxTemplate, error) {
	if name == "" {
		return SandboxTemplate{}, ErrNotFound
	}
	rows, err := s.sandboxTemplates(ctx, accountID, name)
	if err != nil {
		return SandboxTemplate{}, err
	}
	if len(rows) == 0 {
		return SandboxTemplate{}, ErrNotFound
	}
	return rows[0], nil
}

func (s *Store) sandboxTemplates(ctx context.Context, accountID, name string) ([]SandboxTemplate, error) {
	rows, err := s.q.ListSandboxTemplates(ctx, s.pool, sqlc.ListSandboxTemplatesParams{AccountID: accountID, Name: name})
	if err != nil {
		return nil, fmt.Errorf("list sandbox templates: %w", err)
	}
	out := make([]SandboxTemplate, 0, len(rows))
	for _, r := range rows {
		t := SandboxTemplate{ID: r.ID, AccountID: accountID, Name: r.Name, AppID: r.AppID, AppSlug: r.AppSlug,
			SandboxTemplateSpec: SandboxTemplateSpec{StartCmd: r.StartCmd, ReadyCmd: r.ReadyCmd, ReadyTimeoutSec: int(r.ReadyTimeoutS)},
			CreatedAt:           r.CreatedAt.Time.UTC(), UpdatedAt: r.UpdatedAt.Time.UTC(),
			BuildID: r.BuildID, BuildStatus: r.BuildStatus, BuildError: r.BuildError,
			LiveDeploymentID: r.LiveDeploymentID, LiveRootfsKey: r.LiveRootfsKey}
		if r.LiveBuiltAt.Valid {
			at := r.LiveBuiltAt.Time.UTC()
			t.LiveBuiltAt = &at
		}
		out = append(out, t)
	}
	return out, nil
}
