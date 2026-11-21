package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) taskRehearsalProvisioner() *taskrehearsal.Provisioner {
	if s.rehearsalPostgres == nil {
		return nil
	}
	return &taskrehearsal.Provisioner{
		AdmissionEnabled: s.rehearsalCommandsEnabled && s.rehearsalNetworkEnabled &&
			s.rehearsalSimulationEnabled && s.rehearsalWorkersEnabled && s.rehearsalPostgresEnabled,
		Store: s.store, Postgres: s.rehearsalPostgres, BuildApp: s.buildApp,
		PersistEnvironment: s.persistScenarioParticipantSecrets,
		PersistDatabaseURL: func(ctx context.Context, account state.Account, app state.App, url string) error {
			return s.persistScenarioParticipantSecrets(ctx, account, app, map[string]string{"DATABASE_URL": url})
		},
	}
}
