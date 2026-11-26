package mcpmanagement

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Why a database was awake (ADR-20261125231222). Like the read-only query
// tools, it names an app: the database must be bound to it in the selected
// scope, so app consent bounds which databases an agent can inspect. It reads
// control-plane records only and never wakes the database.

type postgresActivityArgs struct {
	App         string `json:"app" jsonschema:"App slug the database is bound to"`
	Project     string `json:"project,omitempty" jsonschema:"Project slug for a named environment"`
	Environment string `json:"environment,omitempty" jsonschema:"Named environment; omit for default"`
	Database    string `json:"database" jsonschema:"Database ID or name from gregale_list_databases"`
	Since       string `json:"since,omitempty" jsonschema:"Period: a duration such as 24h or 7d, or an RFC 3339 time; default 24h, at most 30 days"`
}

func registerPostgresActivity(s *mcp.Server, m *management) {
	if !m.hasCapability(api.ScopeManagedPostgresRead) {
		return
	}
	register(s, m, "gregale_get_postgres_activity", "Explain why a database bound to the app was awake: its compute starts with a likely cause each (wake-with-app, cron, request, app start, running app, external client), awake time per cause, and hints with the CLI command that acts on each. Causes are best guesses from timing; starts are kept 30 days. Reads records only; never wakes the database.", m.getPostgresActivity)
}

func (m *management) getPostgresActivity(ctx context.Context, a postgresActivityArgs) (any, error) {
	if len(a.Since) > 64 {
		return nil, invalid("invalid_argument", "since must be a duration such as 7d or an RFC 3339 time")
	}
	id, err := m.boundDatabase(ctx, a.App, a.Project, a.Environment, a.Database)
	if err != nil {
		return nil, err
	}
	out, err := m.client.GetManagedPostgresActivity(ctx, id, a.Since)
	if err != nil {
		return nil, err
	}
	if out.DatabaseID != id {
		return nil, invalid("invalid_response", "activity names a different database")
	}
	return out, nil
}
