package apidserver

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) persistScenarioParticipantSecrets(ctx context.Context, acct state.Account, app state.App, values map[string]string) error {
	limits, _ := api.LimitsFor(acct.Plan)
	for key, value := range values {
		if p := s.checkSecretQuota(ctx, acct, app, "default", key, limits); p != nil {
			return fmt.Errorf("participant secret quota: %s", p.Detail)
		}
		if p := s.sealAndPersist(ctx, acct, app, "default", key, value, api.SecretClassEphemeral, limits); p != nil {
			return fmt.Errorf("participant secret delivery: %s", p.Detail)
		}
	}
	return nil
}

func (s *server) bindScenarioPostgresParticipants(ctx context.Context, acct state.Account, member state.ScenarioTestMember) error {
	if member.ManagedWorkJSON == "" {
		return nil
	}
	plan := rehearsalwork.Decode(member.ManagedWorkJSON)
	for _, p := range plan.Participants {
		if p.Workload == member.Workload || p.Postgres == "" {
			continue
		}
		app, e := s.store.ScenarioTestAppByWorkload(ctx, acct.ID, member.RunID, p.Workload)
		if e != nil {
			return e
		}
		connection, e := s.rehearsalPostgres.BindParticipant(ctx, acct.ID, member.RunID, app.ID, p.Workload+".svc.gregale")
		if e != nil {
			return e
		}
		if e = s.persistScenarioParticipantSecrets(ctx, acct, app, map[string]string{"DATABASE_URL": connection}); e != nil {
			return e
		}
	}
	return nil
}

func (s *server) bindScenarioObjectParticipants(ctx context.Context, acct state.Account, member state.ScenarioTestMember, values map[string]string) error {
	if member.ManagedWorkJSON == "" {
		return nil
	}
	plan := rehearsalwork.Decode(member.ManagedWorkJSON)
	for _, p := range plan.Participants {
		if p.Workload == member.Workload || p.Objects == "" {
			continue
		}
		app, e := s.store.ScenarioTestAppByWorkload(ctx, acct.ID, member.RunID, p.Workload)
		if e != nil {
			return e
		}
		credential := make(map[string]string, len(values))
		for k, v := range values {
			credential[k] = v
		}
		credential["AWS_ACCESS_KEY_ID"], credential["AWS_SECRET_ACCESS_KEY"] = rehearsalwork.ObjectCredentials(values["AWS_SECRET_ACCESS_KEY"], app.ID)
		credential["AWS_ENDPOINT_URL_S3"] = "http://" + p.Workload + ".svc.gregale:10084"
		if e = s.persistScenarioParticipantSecrets(ctx, acct, app, credential); e != nil {
			return e
		}
	}
	return nil
}
