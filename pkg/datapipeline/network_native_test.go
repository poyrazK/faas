package datapipeline

import (
	"net/url"
	"os"
	"testing"
)

func TestNativeSubscriberConnectionFallbackUsesVerifiedOriginalHost(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY);INSERT INTO public.orders VALUES(1)`)
	ctx := t.Context()
	u, err := url.Parse(f.raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = "localhost:5432"
	q := u.Query()
	q.Set("connect_timeout", "1")
	u.RawQuery = q.Encode()
	// Exercise libpq itself inside the native PostgreSQL fixture. The first
	// reviewed address has no route; the second reaches its real TLS listener.
	pinned, err := nativeSourceConninfo(u, []string{"::2", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	native, err := url.Parse(pinned)
	if err != nil {
		t.Fatal(err)
	}
	q = native.Query()
	q.Set("sslrootcert", os.Getenv("DATA_PIPELINE_TEST_SUBSCRIBER_CA"))
	native.RawQuery = q.Encode()
	if _, err = f.target.Exec(ctx, `CREATE EXTENSION dblink`); err != nil {
		t.Fatal(err)
	}
	var database string
	if err = f.target.QueryRow(ctx, `SELECT database FROM dblink($1,'SELECT current_database()') AS remote(database text)`, native.String()).Scan(&database); err != nil || database != u.Path[1:] {
		t.Fatal("native connection did not fall back with verified TLS", database, err)
	}
}
