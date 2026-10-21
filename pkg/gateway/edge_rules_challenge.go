package gateway

// kind=challenge (ADR-831 amendment 7): a matched request without a valid
// pass is answered at the edge. A browser navigation gets a page that solves
// a proof of work and posts it to ChallengeSolvePath; the gateway checks it
// and sets a signed pass cookie. Any other request gets 403
// challenge_required. Requests with a verified API key, consumer key or JWT
// subject are never challenged. The gateway keeps no state: tokens and passes
// are HMAC-signed and bound to the client's address prefix and User-Agent.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/bits"
	"net"
	"net/http"
	"strings"
	"time"
)

// EdgeRuleChallengeResolved is the compiled kind=challenge matcher payload.
type EdgeRuleChallengeResolved struct {
	EdgeRuleCondition
	ID             string
	AccountID      string
	AppID          string
	Priority       int
	PathGlob       string
	Methods        map[string]bool
	MatchHeaders   map[string]string
	Difficulty     int
	PassTTLSeconds int
}

// ChallengeEdgeRuleMatcher is optional so existing matchers do not have to
// grow when the challenge kind is not wired.
type ChallengeEdgeRuleMatcher interface {
	// MatchChallenge returns the first applicable challenge rule for the
	// request, or nil.
	MatchChallenge(ctx context.Context, host, path, method string) *EdgeRuleChallengeResolved
	// HostChallengeRule returns any enabled challenge rule the host's app
	// owns, regardless of path, method or condition, or nil. The solve
	// endpoint uses it: the path only belongs to the gateway on hosts that
	// challenge.
	HostChallengeRule(ctx context.Context, host string) *EdgeRuleChallengeResolved
}

// PickFirstChallengeMatch returns the first priority-ordered challenge rule
// matching the request path, method and optional header conditions. Paths
// match protectively, like the other gates that deny a request.
func PickFirstChallengeMatch(rules []EdgeRuleChallengeResolved, requestPath, method string, requestHeaders ...http.Header) *EdgeRuleChallengeResolved {
	for i := range rules {
		rule := &rules[i]
		if len(requestHeaders) > 0 && !RequestHeaderConditionsMatch(rule.MatchHeaders, requestHeaders[0]) {
			continue
		}
		if rule.Methods != nil && !rule.Methods[method] {
			continue
		}
		if rule.PathGlob != "" {
			if ok, _ := protectivePathMatch(rule.PathGlob, requestPath); !ok {
				continue
			}
		}
		return rule
	}
	return nil
}

// FirstEnforcedChallengeRule returns the first challenge rule the request's
// app owns that is not in log mode, regardless of path or condition. Only
// an enforced rule ever serves a challenge, so only then does the solve
// endpoint belong to the gateway.
func FirstEnforcedChallengeRule(ctx context.Context, rules []EdgeRuleChallengeResolved) *EdgeRuleChallengeResolved {
	owned := OwnedEdgeRules(ctx, rules, func(r *EdgeRuleChallengeResolved) string { return r.AccountID })
	for i := range owned {
		if !owned[i].LogOnly {
			return &owned[i]
		}
	}
	return nil
}

const (
	// ChallengeSolvePath receives the solved proof of work on hosts with a
	// challenge rule.
	ChallengeSolvePath = "/.well-known/gregale-challenge"
	// ChallengeCookieName holds the signed pass.
	ChallengeCookieName = "gregale_challenge"

	challengeTokenPrefix = "c1."
	challengePassPrefix  = "p1."
	challengeMACBytes    = 16
	challengeMaxField    = 2048
)

// DeriveEdgeChallengeKey derives the challenge signing key from a secret
// every gateway shares, so a pass issued by one gateway is honoured by all.
func DeriveEdgeChallengeKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("gregale edge challenge v1"))
	return mac.Sum(nil)
}

// challengeBinding is what a token and a pass are bound to: the client's
// address prefix (/24 for IPv4, /48 for IPv6) and a hash of its User-Agent.
// A client that moves within its prefix keeps its pass.
type challengeBinding struct {
	prefix []byte
	ua     [8]byte
}

func bindingFor(r *http.Request) challengeBinding {
	var b challengeBinding
	if ip := challengeClientIP(r); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			b.prefix = append([]byte{4}, v4[:3]...)
		} else {
			b.prefix = append([]byte{6}, ip.To16()[:6]...)
		}
	}
	sum := sha256.Sum256([]byte(r.Header.Get("User-Agent")))
	copy(b.ua[:], sum[:8])
	return b
}

// challengeClientIP is the trusted client address: the single
// X-Forwarded-For hop the public listener stamps, or the peer address when
// the gateway is reached directly.
func challengeClientIP(r *http.Request) net.IP {
	if ip, ok := clientIPFromTrustedXFF(r); ok {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

// challengeToken is the signed challenge a page carries.
type challengeToken struct {
	AppID      string `json:"a"`
	RuleID     string `json:"r"`
	Nonce      []byte `json:"n"`
	Difficulty int    `json:"d"`
	PassTTL    int    `json:"t"`
	Expires    int64  `json:"e"`
}

// challengePass is the signed pass a cookie carries.
type challengePass struct {
	AppID   string `json:"a"`
	Expires int64  `json:"e"`
}

// sealChallenge encodes v as prefix + base64(json) + "." + base64(mac),
// where the MAC also covers the binding. The purpose string keeps a token
// from being accepted as a pass.
func sealChallenge(key []byte, purpose, prefix string, v any, b challengeBinding) string {
	raw, _ := json.Marshal(v)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return prefix + payload + "." + base64.RawURLEncoding.EncodeToString(challengeMAC(key, purpose, payload, b))
}

// openChallenge reverses sealChallenge, reporting false for any malformed
// value or a MAC that does not match this key and binding.
func openChallenge(key []byte, purpose, prefix, sealed string, b challengeBinding, v any) bool {
	if len(sealed) > challengeMaxField || !strings.HasPrefix(sealed, prefix) {
		return false
	}
	payload, macPart, ok := strings.Cut(sealed[len(prefix):], ".")
	if !ok {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(macPart)
	if err != nil || !hmac.Equal(got, challengeMAC(key, purpose, payload, b)) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	return err == nil && json.Unmarshal(raw, v) == nil
}

func challengeMAC(key []byte, purpose, payload string, b challengeBinding) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(purpose))
	mac.Write([]byte{0})
	mac.Write([]byte(payload))
	mac.Write([]byte{0})
	mac.Write(b.prefix)
	mac.Write([]byte{0})
	mac.Write(b.ua[:])
	return mac.Sum(nil)[:challengeMACBytes]
}

// challengeWorkValid reports whether SHA-256(token + ":" + counter) starts
// with at least difficulty zero bits. The counter is decimal digits only.
func challengeWorkValid(token, counter string, difficulty int) bool {
	if counter == "" || len(counter) > 20 || difficulty < 1 || difficulty > 32 {
		return false
	}
	for i := 0; i < len(counter); i++ {
		if counter[i] < '0' || counter[i] > '9' {
			return false
		}
	}
	sum := sha256.Sum256([]byte(token + ":" + counter))
	return bits.LeadingZeros32(binary.BigEndian.Uint32(sum[:4])) >= difficulty
}

// validPass reports whether the request carries an unexpired pass for app.
func validPass(key []byte, r *http.Request, appID string, now time.Time) bool {
	c, err := r.Cookie(ChallengeCookieName)
	if err != nil {
		return false
	}
	var p challengePass
	if !openChallenge(key, "pass", challengePassPrefix, c.Value, bindingFor(r), &p) {
		return false
	}
	return p.AppID == appID && now.Unix() < p.Expires
}

// isBrowserNavigation reports whether a request is a top-level page load:
// GET or HEAD, with Sec-Fetch-Mode navigate when the browser sends it, or an
// Accept header naming text/html otherwise.
func isBrowserNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return strings.EqualFold(mode, "navigate")
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

// safeReturnPath returns p when it is a path on this host (no scheme, no
// authority, no backslash or control character), else "/".
func safeReturnPath(p string) string {
	if p == "" || len(p) > challengeMaxField || p[0] != '/' || strings.HasPrefix(p, "//") {
		return "/"
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f || p[i] == '\\' {
			return "/"
		}
	}
	return p
}
