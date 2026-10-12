//go:build !no_pg

// adr: 1027
package pgscram

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
)

// TestPostgresAcceptsVerifierAndPassThrough proves the proxy's two claims
// against a real server: PostgreSQL stores our verifier verbatim and accepts
// the password it was made from, and it also accepts a login that knows
// only the ClientKey. It needs DATABASE_URL with a superuser and a pg_hba
// that uses scram-sha-256 for other roles.
func TestPostgresAcceptsVerifierAndPassThrough(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	role := "scram_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	password := "pt-" + uuid.NewString()
	v, err := NewVerifier(password, DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD '"+v.String()+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP ROLE "+role) })
	var stored string
	if err := admin.QueryRow(ctx, "SELECT rolpassword FROM pg_authid WHERE rolname = $1", role).Scan(&stored); err != nil || stored != v.String() {
		t.Fatalf("pg_authid = %q, %v; want our verifier verbatim", stored, err)
	}
	cfg := admin.Config()
	host, port := cfg.Host, cfg.Port

	// The password the verifier was made from logs in normally.
	conn, err := pgx.Connect(ctx, "postgres://"+role+":"+password+"@"+net.JoinHostPort(host, itoa(port))+"/"+cfg.Database+"?sslmode=disable")
	if err != nil {
		t.Fatalf("password login: %v", err)
	}
	_ = conn.Close(ctx)

	// A login holding only the ClientKey, as the proxy does after verifying
	// the client, is accepted too.
	salted, _ := pbkdf2.Key(sha256.New, password, v.Salt, v.Iterations, sha256.Size)
	if err := passThroughLogin(ctx, net.JoinHostPort(host, itoa(port)), role, cfg.Database, hmacSHA256(salted, "Client Key"), v.ServerKey); err != nil {
		t.Fatalf("pass-through login: %v", err)
	}
	if err := passThroughLogin(ctx, net.JoinHostPort(host, itoa(port)), role, cfg.Database, make([]byte, 32), v.ServerKey); err == nil {
		t.Fatal("pass-through with a wrong key was accepted")
	}
}

func itoa(n uint16) string { return strconv.Itoa(int(n)) }

func passThroughLogin(ctx context.Context, addr, user, database string, clientKey, serverKey []byte) error {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = nc.Close() }()
	fe := pgproto3.NewFrontend(nc, nc)
	fe.Send(&pgproto3.StartupMessage{ProtocolVersion: pgproto3.ProtocolVersionNumber, Parameters: map[string]string{"user": user, "database": database}})
	if err := fe.Flush(); err != nil {
		return err
	}
	session, err := NewClientSession(clientKey, serverKey)
	if err != nil {
		return err
	}
	for {
		msg, err := fe.Receive()
		if err != nil {
			return err
		}
		switch m := msg.(type) {
		case *pgproto3.AuthenticationSASL:
			fe.Send(&pgproto3.SASLInitialResponse{AuthMechanism: Mechanism, Data: session.ClientFirst()})
		case *pgproto3.AuthenticationSASLContinue:
			final, err := session.ServerFirst(m.Data)
			if err != nil {
				return err
			}
			fe.Send(&pgproto3.SASLResponse{Data: final})
		case *pgproto3.AuthenticationSASLFinal:
			if err := session.ServerFinal(m.Data); err != nil {
				return err
			}
			continue
		case *pgproto3.ErrorResponse:
			return &pgprotoError{m.Code, m.Message}
		case *pgproto3.ReadyForQuery:
			return nil
		default:
			continue
		}
		if err := fe.Flush(); err != nil {
			return err
		}
	}
}

type pgprotoError struct{ code, msg string }

func (e *pgprotoError) Error() string { return e.code + ": " + e.msg }
