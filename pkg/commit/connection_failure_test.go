package commit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestConnectionFailureClassifiesAuthenticationWithoutRawErrors(t *testing.T) {
	for _, code := range []string{"28P01", "28000", "53300", "08006"} {
		raw := fmt.Errorf("connection includes private-url-sentinel: %w", &pgconn.PgError{Code: code, Message: "private-password-sentinel"})
		err := connectionFailure(raw)
		want := code == "28P01" || code == "28000"
		if errors.Is(err, ErrConnectionAuthentication) != want {
			t.Fatal("lost authentication classification", code, err)
		}
		if strings.Contains(err.Error(), "sentinel") || errors.Unwrap(err) != nil {
			t.Fatal("raw connection details retained", err)
		}
	}
}

func TestConnectionFailureClassifiesRetryableTransport(t *testing.T) {
	for _, fault := range []error{
		ErrConnectionTransport, io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET,
		context.DeadlineExceeded, &pgconn.PgError{Code: "57P03"},
		&net.DNSError{Err: "private resolver details", IsTemporary: true},
		&net.DNSError{Err: "private resolver details", IsTimeout: true},
	} {
		err := connectionFailure(fmt.Errorf("private transport details: %w", fault))
		if !errors.Is(err, ErrConnectionTransport) || errors.Unwrap(err) != nil {
			t.Fatalf("transport failure classification: %v", err)
		}
	}
	for _, fault := range []error{
		&pgconn.PgError{Code: "28P01"}, &pgconn.PgError{Code: "53300"},
		&net.DNSError{Err: "host not found", IsNotFound: true},
		errors.New("certificate rejected"), errors.New("database destination not qualified"),
	} {
		if err := connectionFailure(fault); errors.Is(err, ErrConnectionTransport) {
			t.Fatalf("unexpected expedited retry for %T", fault)
		}
	}
}

func TestOpenPoolPreservesTemporaryDNSFailureClassification(t *testing.T) {
	const host = "commit-dns.example.test"
	var queried atomic.Bool
	original := net.DefaultResolver
	t.Cleanup(func() { net.DefaultResolver = original })
	t.Setenv("PGSSLROOTCERT", "")
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			queried.Store(true)
			return nil, &net.DNSError{Err: "private DNS details", Name: host, IsTemporary: true}
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	pool, err := OpenPool(ctx, "postgres://relay:fixture@"+host+"/db?sslmode=verify-full", NetworkPolicy{
		Hosts:    map[string]bool{host: true},
		Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
	})
	if pool != nil {
		pool.Close()
	}
	if !queried.Load() || !errors.Is(err, ErrConnectionTransport) || errors.Unwrap(err) != nil {
		t.Fatalf("temporary DNS failure: resolver exercised=%t error=%v", queried.Load(), err)
	}
}
