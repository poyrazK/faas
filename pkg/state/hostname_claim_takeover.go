package state

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ClaimKind names the namespace a takeover token proves a claim in.
type ClaimKind string

const (
	ClaimKindCustomDomain   ClaimKind = "custom_domain"
	ClaimKindTenantHostname ClaimKind = "tenant_hostname"
)

// ClaimTakeoverToken is the TXT value with which accountID proves control of
// hostname over another account's unverified claim (ADR-20261020090500). It
// is published at the name's ordinary challenge owner
// (CustomDomainChallengeName). The value is deterministic, so retries, apid
// replicas and a record left from an earlier attempt agree, and it is bound
// to the claimant, so the holder's own challenge never counts as another
// account's proof. It need not be secret: only the name's DNS controller can
// publish it.
func ClaimTakeoverToken(kind ClaimKind, accountID, hostname string) string {
	sum := sha256.Sum256([]byte("gregale-claim-takeover-v1\x00" + string(kind) + "\x00" + accountID + "\x00" + strings.ToLower(strings.TrimSpace(hostname))))
	return hex.EncodeToString(sum[:16])
}
