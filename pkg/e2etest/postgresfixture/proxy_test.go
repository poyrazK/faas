package postgresfixture

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestProxySlowConnectionDoesNotDelayReplacement(t *testing.T) {
	data := pgtest.OpenDatabase(t)
	if err := data.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	proxy := OpenProxy(t, data, "127.0.0.1", 0)
	config := data.Config().ConnConfig.Copy()
	config.Host, config.Port = proxy.Host, proxy.Port
	proxy.DelayNextConnection(4 * time.Second)
	first, cancel := context.WithTimeout(t.Context(), 120*time.Millisecond)
	connection, err := pgx.ConnectConfig(first, config.Copy())
	if connection != nil {
		_ = connection.Close(context.Background())
	}
	expired := first.Err() != nil
	cancel()
	if err == nil || !expired {
		t.Fatal("first provider connection did not reach its deadline")
	}
	next, stop := context.WithTimeout(t.Context(), 2*time.Second)
	defer stop()
	replacement, err := pgx.ConnectConfig(next, config.Copy())
	if err != nil {
		t.Fatal("one-shot fault also blocked the healthy replacement")
	}
	defer func() { _ = replacement.Close(context.Background()) }()
	if err := replacement.Ping(next); err != nil {
		t.Fatal(err)
	}
}
