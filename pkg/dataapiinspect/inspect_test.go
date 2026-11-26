package dataapiinspect

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func testTrust(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestTargetDigestBindsEveryConnectionCoordinateAndNonce(t *testing.T) {
	nonce := strings.Repeat("a", 64)
	base, err := TargetDigest(nonce, "database.example", 5432, "app", "login")
	if err != nil || len(base) != 64 {
		t.Fatalf("invalid digest %q %v", base, err)
	}
	for _, other := range []struct {
		nonce, host, database, login string
		port                         uint16
	}{
		{strings.Repeat("b", 64), "database.example", "app", "login", 5432},
		{nonce, "other.example", "app", "login", 5432},
		{nonce, "database.example", "app", "login", 5433},
		{nonce, "database.example", "other", "login", 5432},
		{nonce, "database.example", "app", "other", 5432},
		{nonce, "database.example", "ap", "plogin", 5432},
	} {
		got, err := TargetDigest(other.nonce, other.host, other.port, other.database, other.login)
		if err != nil || got == base {
			t.Fatalf("target change did not change digest: %v", err)
		}
	}
	for _, bad := range []string{"", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 62)} {
		if _, err := TargetDigest(bad, "database.example", 5432, "app", "login"); err == nil {
			t.Fatal("accepted invalid nonce")
		}
	}
}

func TestInspectionRequiresVerifiedTLSAndSanitizesFailures(t *testing.T) {
	const secret = "postgres://private-user:private-password@private-host:5432/app"
	nonce := strings.Repeat("a", 64)
	ca := testTrust(t)
	for _, dsn := range []string{secret, secret + "?sslmode=disable", secret + "?sslmode=require", secret + "?sslmode=verify-ca", secret + "?sslmode=verify-full&options=-c%20role%3Dpostgres", secret + strings.Repeat("a", 32<<10)} {
		calls := 0
		_, err := run(context.Background(), dsn, "notes", nonce, ca, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) { calls++; return nil, errors.New(secret) })
		if !errors.Is(err, ErrUnavailable) || calls != 0 || strings.Contains(err.Error(), "private") {
			t.Fatalf("accepted or leaked unsafe connection: calls=%d err=%v", calls, err)
		}
	}
	calls := 0
	_, err := run(context.Background(), secret+"?sslmode=verify-full", "notes", nonce, ca, func(_ context.Context, config *pgx.ConnConfig) (*pgx.Conn, error) {
		calls++
		if config.TLSConfig == nil || config.TLSConfig.RootCAs == nil || config.TLSConfig.InsecureSkipVerify || config.TLSConfig.ServerName != "private-host" || len(config.Fallbacks) != 0 {
			t.Fatal("unsafe TLS configuration")
		}
		return nil, errors.New(secret)
	})
	if !errors.Is(err, ErrUnavailable) || calls != 1 || strings.Contains(err.Error(), "private") {
		t.Fatalf("connection error leaked: calls=%d err=%v", calls, err)
	}
	if _, err := Run(context.Background(), secret+"?sslmode=verify-full", "notes", nonce, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("accepted missing host trust")
	}
}
