package api

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Postgres change workers (ADR-20261112140000): row changes of allowlisted
// table columns are captured into a customer outbox and delivered to one of
// the app's queues.
const (
	// PostgresChangeFeedsMax bounds the feeds of one app.
	PostgresChangeFeedsMax = 20
	// PostgresChangeFeedColumnsMax bounds the allowlisted columns of one feed.
	PostgresChangeFeedColumnsMax = 100
	// PostgresChangesPollDefault is how often the relay reads the outbox.
	PostgresChangesPollDefault = 5 * time.Second
	// PostgresChangesPollMin and PostgresChangesPollMax bound poll_interval.
	PostgresChangesPollMin = time.Second
	PostgresChangesPollMax = 10 * time.Minute
)

// PostgresChangeOperations is the capturable operation vocabulary.
var PostgresChangeOperations = []string{"insert", "update", "delete"}

var postgresChangeFeedName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// PostgresChangeFeed captures allowlisted columns of one table.
type PostgresChangeFeed struct {
	// Name appears in each event's type: database.<name>.<operation>.
	Name string `json:"name" yaml:"name"`
	// Table is the table, optionally schema-qualified (public.orders).
	Table string `json:"table" yaml:"table"`
	// Columns are the only columns an event carries. The primary key must
	// be listed.
	Columns []string `json:"columns" yaml:"columns"`
	// Operations narrows capture; empty means insert, update and delete.
	Operations []string `json:"operations,omitempty" yaml:"operations,omitempty"`
}

// Validate checks the feed's shape. The database checks the table, its
// primary key and that every key column is allowlisted.
func (f PostgresChangeFeed) Validate() error {
	if !postgresChangeFeedName.MatchString(f.Name) {
		return fmt.Errorf("feed name %q must be 1-40 lowercase letters, digits or _ and start with a letter", f.Name)
	}
	if f.Table == "" || len(f.Table) > 256 || !utf8.ValidString(f.Table) || strings.IndexFunc(f.Table, unicode.IsControl) >= 0 {
		return fmt.Errorf("feed %s: table must be 1-256 printable characters", f.Name)
	}
	if len(f.Columns) == 0 || len(f.Columns) > PostgresChangeFeedColumnsMax {
		return fmt.Errorf("feed %s: columns must list 1-%d columns", f.Name, PostgresChangeFeedColumnsMax)
	}
	seen := map[string]bool{}
	for _, c := range f.Columns {
		// NAMEDATALEN-1 bytes is PostgreSQL's identifier ceiling.
		if c == "" || len(c) > 63 || !utf8.ValidString(c) || strings.IndexFunc(c, unicode.IsControl) >= 0 {
			return fmt.Errorf("feed %s: column %q is not a valid column name", f.Name, c)
		}
		if seen[c] {
			return fmt.Errorf("feed %s: column %q is listed twice", f.Name, c)
		}
		seen[c] = true
	}
	ops := map[string]bool{}
	for _, op := range f.Operations {
		if !slices.Contains(PostgresChangeOperations, op) {
			return fmt.Errorf("feed %s: operation %q must be one of insert, update, delete", f.Name, op)
		}
		if ops[op] {
			return fmt.Errorf("feed %s: operation %q is listed twice", f.Name, op)
		}
		ops[op] = true
	}
	return nil
}

// EffectiveOperations is Operations, or all of them when it is empty.
func (f PostgresChangeFeed) EffectiveOperations() []string {
	if len(f.Operations) == 0 {
		return PostgresChangeOperations
	}
	return f.Operations
}

// ValidatePostgresChangeFeeds checks an app's feed list.
func ValidatePostgresChangeFeeds(feeds []PostgresChangeFeed) error {
	if len(feeds) == 0 || len(feeds) > PostgresChangeFeedsMax {
		return fmt.Errorf("feeds must list 1-%d feeds", PostgresChangeFeedsMax)
	}
	names := map[string]bool{}
	for _, f := range feeds {
		if err := f.Validate(); err != nil {
			return err
		}
		if names[f.Name] {
			return fmt.Errorf("feed name %q is used twice", f.Name)
		}
		names[f.Name] = true
	}
	return nil
}

// PutPostgresChangesRequest declares an app's Postgres change capture
// (PUT /v1/apps/{slug}/postgres-changes).
type PutPostgresChangesRequest struct {
	// Database names a managed database of the account: Gregale installs
	// capture into it. Empty means a database you run (BYO): install with
	// `gregale postgres-changes install`.
	Database string `json:"database,omitempty"`
	// QueueName is the app's queue binding that receives the changes.
	QueueName string `json:"queue_name"`
	// PollIntervalSeconds is how often Gregale reads the outbox (1-600,
	// default 5). 300 or more lets an idle managed database suspend.
	PollIntervalSeconds int                  `json:"poll_interval_seconds,omitempty"`
	Feeds               []PostgresChangeFeed `json:"feeds"`
}

// Validate checks the request's shape.
func (r PutPostgresChangesRequest) Validate() error {
	if !ValidQueueBindingName(r.QueueName) {
		return errors.New("queue_name must name one of the app's queue bindings ([a-z][a-z0-9-]{0,62})")
	}
	if len(r.Database) > 256 {
		return errors.New("database must be at most 256 bytes")
	}
	if r.PollIntervalSeconds != 0 {
		d := time.Duration(r.PollIntervalSeconds) * time.Second
		if d < PostgresChangesPollMin || d > PostgresChangesPollMax {
			return errors.New("poll_interval_seconds must be 1-600")
		}
	}
	return ValidatePostgresChangeFeeds(r.Feeds)
}

// PollInterval is the effective relay poll interval.
func (r PutPostgresChangesRequest) PollInterval() time.Duration {
	if r.PollIntervalSeconds == 0 {
		return PostgresChangesPollDefault
	}
	return time.Duration(r.PollIntervalSeconds) * time.Second
}

// PostgresChangesResponse is an app's Postgres change capture and its relay
// health.
type PostgresChangesResponse struct {
	SourceID            string               `json:"source_id"`
	QueueName           string               `json:"queue_name"`
	Database            string               `json:"database,omitempty"`
	Managed             bool                 `json:"managed"`
	PollIntervalSeconds int                  `json:"poll_interval_seconds"`
	Feeds               []PostgresChangeFeed `json:"feeds"`
	Enabled             bool                 `json:"enabled"`
	// Connected reports whether Gregale holds the relay's login. A BYO
	// source connects when `gregale postgres-changes install` runs.
	Connected bool `json:"connected"`
	// RelayRole is the least-privilege login the install creates for
	// Gregale's relay.
	RelayRole       string     `json:"relay_role"`
	RelayStatus     string     `json:"relay_status"`
	LastCheckedAt   *time.Time `json:"last_checked_at,omitempty"`
	PendingEvents   *int64     `json:"pending_events,omitempty"`
	BlockedEvents   *int64     `json:"blocked_events,omitempty"`
	OldestPendingAt *time.Time `json:"oldest_pending_at,omitempty"`
}
