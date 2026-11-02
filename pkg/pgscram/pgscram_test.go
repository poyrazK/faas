// adr: 1027
package pgscram

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// TestRFC7677Vector checks the server side against RFC 7677's example
// exchange for password "pencil".
func TestRFC7677Vector(t *testing.T) {
	salt, _ := base64.StdEncoding.DecodeString("W22ZaJ0SNY7soEsUEjb6gQ==")
	v, err := verifierWithSalt("pencil", salt, 4096)
	if err != nil {
		t.Fatal(err)
	}
	saved := newNonce
	t.Cleanup(func() { newNonce = saved })
	newNonce = func() (string, error) { return "%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0", nil }
	s := NewServerSession(v)
	first, err := s.ClientFirst([]byte("n,,n=user,r=rOprNGfwEbeRWgbNEkqO"))
	if err != nil || string(first) != "r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096" {
		t.Fatalf("server-first = %q, %v", first, err)
	}
	final, err := s.ClientFinal([]byte("c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ="))
	if err != nil || string(final) != "v=6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=" {
		t.Fatalf("server-final = %q, %v", final, err)
	}
}

// clientKeyFor derives what a real client proves for password and v.
func clientKeyFor(t *testing.T, password string, v Verifier) []byte {
	t.Helper()
	salted, err := pbkdf2.Key(sha256.New, password, v.Salt, v.Iterations, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	return hmacSHA256(salted, "Client Key")
}

// TestPassThroughRoundTrip runs a client against the proxy's server side,
// then the proxy's client side against a server holding the same verifier,
// using only the ClientKey the first exchange recovered.
func TestPassThroughRoundTrip(t *testing.T) {
	v, err := NewVerifier("Sup3r-Secret-app-password", DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := NewClientSession(clientKeyFor(t, "Sup3r-Secret-app-password", v), v.ServerKey)
	proxy := NewServerSession(v)
	first, err := proxy.ClientFirst(client.ClientFirst())
	if err != nil {
		t.Fatal(err)
	}
	clientFinal, err := client.ServerFirst(first)
	if err != nil {
		t.Fatal(err)
	}
	serverFinal, err := proxy.ClientFinal(clientFinal)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ServerFinal(serverFinal); err != nil {
		t.Fatalf("client rejected the proxy: %v", err)
	}
	// Second leg: the proxy logs in to "PostgreSQL" with the recovered key.
	backendLeg, _ := NewClientSession(proxy.ClientKey(), v.ServerKey)
	postgres := NewServerSession(v)
	pf, err := postgres.ClientFirst(backendLeg.ClientFirst())
	if err != nil {
		t.Fatal(err)
	}
	cf, err := backendLeg.ServerFirst(pf)
	if err != nil {
		t.Fatal(err)
	}
	sf, err := postgres.ClientFinal(cf)
	if err != nil {
		t.Fatalf("backend rejected the pass-through proof: %v", err)
	}
	if err := backendLeg.ServerFinal(sf); err != nil {
		t.Fatal(err)
	}
}

func TestServerSessionRejects(t *testing.T) {
	v, _ := NewVerifier("right-password", DefaultIterations)
	run := func(password string, mutate func(string) string) error {
		client, _ := NewClientSession(clientKeyFor(t, password, v), v.ServerKey)
		s := NewServerSession(v)
		first, err := s.ClientFirst(client.ClientFirst())
		if err != nil {
			return err
		}
		final, _ := client.ServerFirst(first)
		_, err = s.ClientFinal([]byte(mutate(string(final))))
		return err
	}
	same := func(s string) string { return s }
	if err := run("right-password", same); err != nil {
		t.Fatalf("right password: %v", err)
	}
	if err := run("wrong-password", same); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("wrong password = %v", err)
	}
	if err := run("right-password", func(s string) string { return strings.Replace(s, ",r=", ",r=X", 1) }); !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("tampered nonce = %v", err)
	}
	if err := run("right-password", func(s string) string { return strings.Replace(s, "c=biws", "c=eSws", 1) }); !errors.Is(err, ErrMalformed) {
		t.Fatalf("tampered channel binding header = %v", err)
	}
	s := NewServerSession(v)
	if _, err := s.ClientFirst([]byte("p=tls-server-end-point,,n=,r=abc")); !errors.Is(err, ErrMalformed) {
		t.Fatalf("channel binding request = %v, want ErrMalformed", err)
	}
	if _, err := s.ClientFinal([]byte("c=biws,r=x,p=AAAA")); !errors.Is(err, ErrMalformed) {
		t.Fatalf("final before first = %v", err)
	}
}

func TestVerifierFormat(t *testing.T) {
	v, err := NewVerifier("abc-DEF_123", 4096)
	if err != nil {
		t.Fatal(err)
	}
	s := v.String()
	if !strings.HasPrefix(s, "SCRAM-SHA-256$4096:") {
		t.Fatalf("verifier = %s", s)
	}
	back, err := ParseVerifier(s)
	if err != nil || back.String() != s {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	for _, bad := range []string{"", "md5abc", "SCRAM-SHA-256$x:AA==$AA==:AA==", "SCRAM-SHA-256$4096:AA==$short:short"} {
		if _, err := ParseVerifier(bad); err == nil {
			t.Errorf("ParseVerifier(%q) accepted", bad)
		}
	}
	if _, err := NewVerifier("has space", 4096); err == nil {
		t.Fatal("password with a space accepted")
	}
	if _, err := NewVerifier("ok", 10); err == nil {
		t.Fatal("weak iteration count accepted")
	}
}
