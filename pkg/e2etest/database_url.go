package e2etest

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// DatabaseURL follows the pool's actual clone database and isolated schema.
// pgx ConnString alone retains its original parsed DSN after Config is edited.
func DatabaseURL(pool *pgxpool.Pool) string {
	return isolatedDatabaseURL(pool.Config().ConnString(), pool)
}

func isolatedDatabaseURL(base string, pool *pgxpool.Pool) string {
	dsn := injectDatabase(base, pool.Config().ConnConfig.Database)
	schema := pgtest.SchemaOf(pool)
	if schema == "" {
		return dsn
	}
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err == nil {
			q := u.Query()
			q.Set("search_path", schema)
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	// libpq keyword values use backslash escaping inside single quotes.
	escaped := strings.ReplaceAll(strings.ReplaceAll(schema, `\`, `\\`), `'`, `\'`)
	return dsn + " search_path='" + escaped + "'"
}

// DatabaseURLViaTCP changes only the transport endpoint of the isolated DSN.
// PostgreSQL TLS and authentication options remain intact through the proxy.
func DatabaseURLViaTCP(pool *pgxpool.Pool, host string, port uint16) (string, error) {
	if net.ParseIP(host) == nil || port == 0 {
		return "", fmt.Errorf("e2etest: proxy requires a numeric IP and port")
	}
	dsn := DatabaseURL(pool)
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		u.Host = net.JoinHostPort(host, strconv.Itoa(int(port)))
		q := u.Query()
		q.Del("host")
		q.Del("port")
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	// The validated endpoint cannot contain keyword separators or quotes.
	return dsn + " host=" + host + " port=" + strconv.Itoa(int(port)), nil
}
