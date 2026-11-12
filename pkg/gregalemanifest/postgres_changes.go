package gregalemanifest

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// PostgresChangesSpec captures Postgres row changes into one of the app's
// queue bindings (ADR-20261112140000):
//
//	postgres_changes:
//	  database: shop              # a managed database; or connection_url_env
//	  queue_name: order-changes   # one of queue_bindings
//	  poll_interval: 5s
//	  feeds:
//	    - name: orders
//	      table: public.orders
//	      columns: [id, status]
type PostgresChangesSpec struct {
	// Database names a managed database: Gregale installs capture into it.
	Database string `yaml:"database,omitempty"`
	// ConnectionURLEnv names the local environment variable holding the
	// owner connection URL of a database you run. `gregale deploy` and
	// `gregale postgres-changes install` read it on your machine; it is never
	// uploaded.
	ConnectionURLEnv string                   `yaml:"connection_url_env,omitempty"`
	QueueName        string                   `yaml:"queue_name"`
	PollInterval     string                   `yaml:"poll_interval,omitempty"`
	Feeds            []api.PostgresChangeFeed `yaml:"feeds"`
}

var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// Validate checks the block against the manifest's queue bindings.
func (p *PostgresChangesSpec) Validate(bindings []QueueBinding) error {
	if p == nil {
		return nil
	}
	if (p.Database == "") == (p.ConnectionURLEnv == "") {
		return errors.New("set exactly one of database (managed) or connection_url_env (a database you run)")
	}
	if p.ConnectionURLEnv != "" && !envVarName.MatchString(p.ConnectionURLEnv) {
		return fmt.Errorf("connection_url_env %q must be an environment variable name", p.ConnectionURLEnv)
	}
	found := false
	for _, b := range bindings {
		if b.QueueName == p.QueueName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("queue_name %q must be the queue_name of one of this app's queue_bindings", p.QueueName)
	}
	req, err := p.Request()
	if err != nil {
		return err
	}
	return req.Validate()
}

// Request is the API request this block declares.
func (p *PostgresChangesSpec) Request() (api.PutPostgresChangesRequest, error) {
	req := api.PutPostgresChangesRequest{Database: p.Database, QueueName: p.QueueName, Feeds: p.Feeds}
	if s := strings.TrimSpace(p.PollInterval); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return req, fmt.Errorf("poll_interval %q: %w", p.PollInterval, err)
		}
		if d < api.PostgresChangesPollMin || d > api.PostgresChangesPollMax || d%time.Second != 0 {
			return req, fmt.Errorf("poll_interval %q must be whole seconds from 1s to 10m", p.PollInterval)
		}
		req.PollIntervalSeconds = int(d / time.Second)
	}
	return req, nil
}
