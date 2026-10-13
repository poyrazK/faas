// Package pgscram implements PostgreSQL's SCRAM-SHA-256 (RFC 5802/7677) for
// the database proxy (ADR-1027). It builds verifiers in pg_authid's format,
// verifies a client against a verifier without the password, and logs in to
// PostgreSQL with the ClientKey recovered from that client's proof (SCRAM
// pass-through). Passwords are only ever hashed into verifiers.
package pgscram

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Mechanism is the SASL mechanism name. The proxy never offers the
// channel-binding variant: its TLS session ends at the proxy.
const Mechanism = "SCRAM-SHA-256"

// DefaultIterations matches PostgreSQL's scram_iterations default.
const DefaultIterations = 4096

var (
	ErrMalformed     = errors.New("pgscram: malformed message")
	ErrAuthFailed    = errors.New("pgscram: authentication failed")
	ErrNonceMismatch = errors.New("pgscram: nonce mismatch")
)

// Verifier is a role's stored SCRAM secret, as in pg_authid.rolpassword.
type Verifier struct {
	Iterations int
	Salt       []byte
	StoredKey  []byte
	ServerKey  []byte
}

// NewVerifier hashes password with a fresh 16-byte salt. Passwords must be
// ASCII without control characters, for which SASLprep is the identity.
func NewVerifier(password string, iterations int) (Verifier, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Verifier{}, err
	}
	return NewVerifierWithSalt(password, salt, iterations)
}

// NewVerifierWithSalt hashes password with the given salt (at least 16
// bytes). Callers that must reproduce a verifier exactly, such as a
// deterministic credential re-issue, derive the salt themselves.
func NewVerifierWithSalt(password string, salt []byte, iterations int) (Verifier, error) {
	if iterations < DefaultIterations {
		return Verifier{}, fmt.Errorf("pgscram: %d iterations is below %d", iterations, DefaultIterations)
	}
	if len(salt) < 16 {
		return Verifier{}, errors.New("pgscram: salt must be at least 16 bytes")
	}
	for _, r := range password {
		if r < 0x21 || r > 0x7e {
			return Verifier{}, errors.New("pgscram: password must be printable ASCII without spaces")
		}
	}
	return verifierWithSalt(password, salt, iterations)
}

func verifierWithSalt(password string, salt []byte, iterations int) (Verifier, error) {
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return Verifier{}, err
	}
	clientKey := hmacSHA256(salted, "Client Key")
	stored := sha256.Sum256(clientKey)
	return Verifier{Iterations: iterations, Salt: salt, StoredKey: stored[:], ServerKey: hmacSHA256(salted, "Server Key")}, nil
}

// String renders the verifier in pg_authid format; PostgreSQL accepts it
// verbatim as a role password and stores it unchanged.
func (v Verifier) String() string {
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", v.Iterations, b64(v.Salt), b64(v.StoredKey), b64(v.ServerKey))
}

// ParseVerifier parses pg_authid format.
func ParseVerifier(s string) (Verifier, error) {
	rest, ok := strings.CutPrefix(s, "SCRAM-SHA-256$")
	if !ok {
		return Verifier{}, ErrMalformed
	}
	params, keys, ok := strings.Cut(rest, "$")
	if !ok {
		return Verifier{}, ErrMalformed
	}
	iterText, saltText, ok1 := strings.Cut(params, ":")
	storedText, serverText, ok2 := strings.Cut(keys, ":")
	if !ok1 || !ok2 {
		return Verifier{}, ErrMalformed
	}
	iterations, err := strconv.Atoi(iterText)
	if err != nil || iterations <= 0 {
		return Verifier{}, ErrMalformed
	}
	dec := base64.StdEncoding.DecodeString
	salt, err1 := dec(saltText)
	stored, err2 := dec(storedText)
	server, err3 := dec(serverText)
	if err1 != nil || err2 != nil || err3 != nil || len(salt) == 0 || len(stored) != sha256.Size || len(server) != sha256.Size {
		return Verifier{}, ErrMalformed
	}
	return Verifier{Iterations: iterations, Salt: salt, StoredKey: stored, ServerKey: server}, nil
}

func hmacSHA256(key []byte, msg string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msg))
	return mac.Sum(nil)
}

func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}

// newNonce is a variable so tests can pin RFC 7677's vector.
var newNonce = func() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// attrs parses "k=v,k=v" SCRAM attributes.
func attrs(msg string) map[byte]string {
	out := map[byte]string{}
	for _, part := range strings.Split(msg, ",") {
		if len(part) >= 2 && part[1] == '=' {
			out[part[0]] = part[2:]
		}
	}
	return out
}

// ServerSession verifies one client against a verifier.
type ServerSession struct {
	verifier        Verifier
	gs2Header       string
	clientFirstBare string
	serverFirst     string
	nonce           string
	clientKey       []byte
	serverFinal     string
}

// NewServerSession starts a server-side exchange.
func NewServerSession(v Verifier) *ServerSession {
	return &ServerSession{verifier: v}
}

// ClientFirst consumes the client-first-message and returns server-first.
func (s *ServerSession) ClientFirst(msg []byte) ([]byte, error) {
	text := string(msg)
	// gs2-header: "n,," or "y,," (client supports channel binding, server
	// offered none). "p=" would demand binding the proxy never offered.
	var bare string
	switch {
	case strings.HasPrefix(text, "n,,"), strings.HasPrefix(text, "y,,"):
		s.gs2Header, bare = text[:3], text[3:]
	default:
		return nil, ErrMalformed
	}
	clientNonce := attrs(bare)['r']
	if clientNonce == "" || strings.ContainsAny(clientNonce, ",") {
		return nil, ErrMalformed
	}
	serverNonce, err := newNonce()
	if err != nil {
		return nil, err
	}
	s.clientFirstBare = bare
	s.nonce = clientNonce + serverNonce
	s.serverFirst = fmt.Sprintf("r=%s,s=%s,i=%d", s.nonce, base64.StdEncoding.EncodeToString(s.verifier.Salt), s.verifier.Iterations)
	return []byte(s.serverFirst), nil
}

// ClientFinal verifies the client proof and returns server-final. On
// success ClientKey holds the key the client proved, for pass-through.
func (s *ServerSession) ClientFinal(msg []byte) ([]byte, error) {
	text := string(msg)
	withoutProof, proofText, ok := strings.Cut(text, ",p=")
	if !ok || s.serverFirst == "" {
		return nil, ErrMalformed
	}
	a := attrs(withoutProof)
	if a['r'] != s.nonce {
		return nil, ErrNonceMismatch
	}
	if a['c'] != base64.StdEncoding.EncodeToString([]byte(s.gs2Header)) {
		return nil, ErrMalformed
	}
	proof, err := base64.StdEncoding.DecodeString(proofText)
	if err != nil || len(proof) != sha256.Size {
		return nil, ErrMalformed
	}
	authMessage := s.clientFirstBare + "," + s.serverFirst + "," + withoutProof
	clientKey := xorBytes(proof, hmacSHA256(s.verifier.StoredKey, authMessage))
	stored := sha256.Sum256(clientKey)
	if subtle.ConstantTimeCompare(stored[:], s.verifier.StoredKey) != 1 {
		return nil, ErrAuthFailed
	}
	s.clientKey = clientKey
	s.serverFinal = "v=" + base64.StdEncoding.EncodeToString(hmacSHA256(s.verifier.ServerKey, authMessage))
	return []byte(s.serverFinal), nil
}

// ClientKey is the key the client proved; nil before a successful
// ClientFinal.
func (s *ServerSession) ClientKey() []byte { return s.clientKey }

// ClientSession logs in to PostgreSQL with a ClientKey instead of a
// password. The server's salt and iterations must match the verifier the
// key belongs to, which holds when the proxy and PostgreSQL store the same
// verifier.
type ClientSession struct {
	clientKey       []byte
	serverKey       []byte
	clientFirstBare string
	nonce           string
	authMessage     string
}

// NewClientSession starts a client-side exchange for clientKey; serverKey
// authenticates PostgreSQL's final message.
func NewClientSession(clientKey, serverKey []byte) (*ClientSession, error) {
	nonce, err := newNonce()
	if err != nil {
		return nil, err
	}
	return &ClientSession{clientKey: clientKey, serverKey: serverKey, nonce: nonce}, nil
}

// ClientFirst returns the client-first-message.
func (c *ClientSession) ClientFirst() []byte {
	c.clientFirstBare = "n=,r=" + c.nonce
	return []byte("n,," + c.clientFirstBare)
}

// ServerFirst consumes server-first and returns client-final.
func (c *ClientSession) ServerFirst(msg []byte) ([]byte, error) {
	serverFirst := string(msg)
	a := attrs(serverFirst)
	if !strings.HasPrefix(a['r'], c.nonce) || len(a['r']) == len(c.nonce) || a['s'] == "" || a['i'] == "" {
		return nil, ErrNonceMismatch
	}
	withoutProof := "c=" + base64.StdEncoding.EncodeToString([]byte("n,,")) + ",r=" + a['r']
	c.authMessage = c.clientFirstBare + "," + serverFirst + "," + withoutProof
	stored := sha256.Sum256(c.clientKey)
	proof := xorBytes(c.clientKey, hmacSHA256(stored[:], c.authMessage))
	return []byte(withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof)), nil
}

// ServerFinal checks PostgreSQL's signature, proving it holds the verifier.
func (c *ClientSession) ServerFinal(msg []byte) error {
	sig, ok := strings.CutPrefix(string(msg), "v=")
	if !ok {
		return ErrMalformed
	}
	want := base64.StdEncoding.EncodeToString(hmacSHA256(c.serverKey, c.authMessage))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return ErrAuthFailed
	}
	return nil
}
