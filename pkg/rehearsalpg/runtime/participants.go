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

type participantBackend interface {
	BindParticipant(context.Context, rehearsalpg.Resource, string, string) error
}

// BindParticipant issues a distinct login for an already-authorized workload.
// Membership in the resource's restricted owner role grants only this fixture.
func (s *Service) BindParticipant(ctx context.Context, account, run, app, host string) (string, error) {
	r, e := s.Catalog.GetScenarioPostgres(ctx, account, run)
	if e != nil || r.Status != "ready" || r.OwnerID != s.OwnerID || app == r.AppID {
		return "", ErrFixture
	}
	bound, err := s.Catalog.ScenarioPostgresForApp(ctx, app)
	if err != nil || bound.ID != r.ID || bound.RoleName != rehearsalwork.PostgresRole(r.ID, app) {
		return "", ErrFixture
	}
	b, ok := s.Backend.(participantBackend)
	if !ok {
		return "", ErrFixture
	}
	password := make([]byte, 32)
	if _, e = rand.Read(password); e != nil {
		return "", ErrFixture
	}
	secret := hex.EncodeToString(password)
	role := rehearsalwork.PostgresRole(r.ID, app)
	if e = b.BindParticipant(ctx, r, role, secret); e != nil {
		return "", ErrFixture
	}
	u := url.URL{Scheme: "postgresql", User: url.UserPassword(role, secret), Host: net.JoinHostPort(host, strconv.Itoa(rehearsalpg.ProxyPort)), Path: "/" + r.DatabaseName, RawQuery: "sslmode=disable"}
	return u.String(), nil
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
