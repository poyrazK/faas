package preview

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	faas "github.com/poyrazK/faas/sdk/go"
)

// PostgresPoolConfig contains an explicit managed connection URI. Factories
// must use it without ambient credentials or connection-identity overrides.
type PostgresPoolConfig struct {
	ConnectionString string `json:"-"`
}

func (PostgresPoolConfig) String() string   { return "PostgresPoolConfig{redacted}" }
func (PostgresPoolConfig) GoString() string { return "PostgresPoolConfig{redacted}" }

// PostgresPoolOptions requires readiness validation and owned-pool retirement.
// Validate must establish a connection, check readiness, and release it before
// returning. Hooks must bound their work and honor context cancellation.
// Retire runs once, off the state lock, after the final lease releases. Different
// pools may retire concurrently; failures are sanitized, sticky and not retried.
type PostgresPoolOptions[T any] struct {
	Validate      func(context.Context, T) error
	Retire        func(T) error
	OnRetireError func(error)
}

// PostgresPoolBinding builds and validates each atomic secret snapshot before
// publishing its pool. Register SecretConsumer with SecretReloadCoordinator to
// ACK only after every consumer adopts the revision. Failed adoption disables
// new acquisition; existing leases may finish. No database driver is imported.
type PostgresPoolBinding[T any] struct{ core *clientBinding[T] }

var postgresSecretKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func NewPostgresPoolBinding[T any](key string, factory func(context.Context, PostgresPoolConfig) (T, error), options PostgresPoolOptions[T]) (*PostgresPoolBinding[T], error) {
	if !postgresSecretKey.MatchString(key) || factory == nil || options.Validate == nil || options.Retire == nil {
		return nil, errors.New("faas: invalid postgres pool binding")
	}
	core := &clientBinding[T]{label: "postgres", resource: "pool", reloads: make(chan struct{}, 1),
		retired: make(map[*bindingClient[T]]struct{}), changed: make(chan struct{}),
		validate: options.Validate, retire: options.Retire, onRetireError: options.OnRetireError}
	core.prepare = func(values map[string]string) (func(context.Context) (T, string, error), error) {
		config, err := loadPostgresPoolConfig(values[key])
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (T, string, error) { pool, err := factory(ctx, config); return pool, "", err }, nil
	}
	return &PostgresPoolBinding[T]{core: core}, nil
}

func (b *PostgresPoolBinding[T]) Reload(ctx context.Context, values map[string]string) error {
	return b.core.Reload(ctx, values)
}

func (b *PostgresPoolBinding[T]) Invalidate()                         { b.core.Invalidate() }
func (b *PostgresPoolBinding[T]) SecretConsumer() faas.SecretConsumer { return b.core.SecretConsumer() }

// WaitForRetirement ignores the active pool. Stop reloads/requests and invalidate
// before shutdown. Cancelling this wait does not terminate leased transactions.
func (b *PostgresPoolBinding[T]) WaitForRetirement(ctx context.Context) error {
	return b.core.WaitForRetirement(ctx)
}

func (b *PostgresPoolBinding[T]) Acquire() (*PostgresPoolLease[T], error) {
	lease, err := b.core.acquire()
	if err != nil {
		return nil, err
	}
	return &PostgresPoolLease[T]{lease: lease}, nil
}

// PostgresPoolLease pins a pool through the entire query or transaction. Release
// the driver's checked-out connection before releasing this lease. A lease does
// not extend server-side credential validity or retry/replay database operations.
type PostgresPoolLease[T any] struct{ lease *bindingLease[T] }

func (PostgresPoolLease[T]) String() string   { return "PostgresPoolLease{redacted}" }
func (PostgresPoolLease[T]) GoString() string { return "PostgresPoolLease{redacted}" }
func (PostgresPoolLease[T]) MarshalJSON() ([]byte, error) {
	return []byte(`"PostgresPoolLease{redacted}"`), nil
}

func (l *PostgresPoolLease[T]) Current() (T, error) {
	if l == nil || l.lease == nil {
		var zero T
		return zero, errors.New("faas: postgres lease is unavailable")
	}
	pool, _, err := l.lease.Current()
	return pool, err
}

// Release is idempotent, including shallow copies and concurrent callers.
func (l *PostgresPoolLease[T]) Release() {
	if l != nil && l.lease != nil {
		l.lease.Release()
	}
}

func loadPostgresPoolConfig(value string) (PostgresPoolConfig, error) {
	invalid := errors.New("faas: postgres binding connection URI is incomplete or invalid")
	u, err := url.Parse(value)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil ||
		u.User.Username() == "" || u.Path == "" || u.Path == "/" || u.Fragment != "" || strings.Contains(value, "#") ||
		strings.IndexFunc(value, func(c rune) bool { return c <= ' ' || c == 0x7f }) >= 0 {
		return PostgresPoolConfig{}, invalid
	}
	password, present := u.User.Password()
	if !present || password == "" || strings.IndexFunc(u.User.Username()+password+u.Path, func(c rune) bool { return c < ' ' || c == 0x7f }) >= 0 ||
		strings.ContainsAny(u.Hostname(), "%/\\\x00\r\n\t ") {
		return PostgresPoolConfig{}, invalid
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return PostgresPoolConfig{}, invalid
		}
	} else {
		// Drivers can otherwise obtain the missing port from ambient PGPORT.
		return PostgresPoolConfig{}, invalid
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return PostgresPoolConfig{}, invalid
	}
	for key := range query {
		switch strings.ToLower(key) {
		case "host", "hostaddr", "port", "user", "username", "password", "dbname", "database", "service", "servicefile", "passfile", "connectionstring", "connection_string", "dsn":
			return PostgresPoolConfig{}, invalid
		}
	}
	return PostgresPoolConfig{ConnectionString: value}, nil
}
