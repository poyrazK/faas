package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalpg/runtime/sqlc"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

type taskParticipantBackend interface {
	bindTaskParticipant(context.Context, rehearsalpg.Resource, string, string, string) error
}

type participantBackend interface {
	BindParticipant(context.Context, rehearsalpg.Resource, string, string) error
}

// BindParticipant issues a distinct login for an already-authorized workload.
// The registered access policy selects a legacy fixture member or a restricted Task role.
func (s *Service) BindParticipant(ctx context.Context, account, run, app, host string) (string, error) {
	r, e := s.Catalog.GetScenarioPostgres(ctx, account, run)
	if e != nil || r.Status != "ready" || r.OwnerID != s.OwnerID || app == r.AppID {
		return "", ErrFixture
	}
	bound, err := s.Catalog.ScenarioPostgresForApp(ctx, app)
	if err != nil || bound.ID != r.ID || !rehearsalwork.PostgresAccess(bound.ParticipantAccess) ||
		bound.RoleName != rehearsalwork.PostgresParticipantRole(r.ID, app, bound.ParticipantAccess) {
		return "", ErrFixture
	}
	password := make([]byte, 32)
	if _, e = rand.Read(password); e != nil {
		return "", ErrFixture
	}
	secret := hex.EncodeToString(password)
	if e = s.bindParticipant(ctx, r, bound, app, secret); e != nil {
		return "", ErrFixture
	}
	u := url.URL{Scheme: "postgresql", User: url.UserPassword(bound.RoleName, secret), Host: net.JoinHostPort(host, strconv.Itoa(rehearsalpg.ProxyPort)), Path: "/" + r.DatabaseName, RawQuery: "sslmode=disable"}
	return u.String(), nil
}

func (s *Service) bindParticipant(ctx context.Context, r, bound rehearsalpg.Resource, app, password string) error {
	if rehearsalwork.TaskPostgresAccess(bound.ParticipantAccess) {
		b, ok := s.Backend.(taskParticipantBackend)
		if !ok {
			return ErrFixture
		}
		return b.bindTaskParticipant(ctx, r, app, password, bound.ParticipantAccess)
	}
	b, ok := s.Backend.(participantBackend)
	if !ok {
		return ErrFixture
	}
	return b.BindParticipant(ctx, r, bound.RoleName, password)
}

func (p *Postgres) BindParticipant(ctx context.Context, r rehearsalpg.Resource, role, password string) error {
	if !p.resourceNames(r) || len(role) != 45 || role[:5] != "grpw_" || len(password) != 64 {
		return ErrFixture
	}
	c, e := p.connect(ctx, "")
	if e != nil {
		return e
	}
	defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
	command, e := sqlc.New().FormatCreateParticipant(ctx, c, sqlc.FormatCreateParticipantParams{RoleName: role, Password: password, OwnerRole: r.RoleName})
	return execFormatted(ctx, c, command, e)
}
