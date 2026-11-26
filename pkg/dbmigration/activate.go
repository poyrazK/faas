package dbmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	Activated               = "activated"
	ActivationReceiptMaxAge = 5 * time.Minute
	MaxActivationRoles      = 128
)

// Activation binds native target admission to one catalog switch. Only the
// switch worker can issue these operations. Roles name unpublished staged
// credentials and are used exclusively while the target is sealed.
type Activation struct {
	SwitchID       string   `json:"switch_id"`
	OperationID    string   `json:"operation_id"`
	SourcePin      string   `json:"source_pin,omitempty"`
	SourceDatabase string   `json:"source_database,omitempty"`
	Roles          []string `json:"roles,omitempty"`
}

func FreshVerification(v *Verification, id string, now time.Time) bool {
	return v != nil && v.ID == id && v.State == Verified && !v.VerifiedAt.IsZero() &&
		!v.VerifiedAt.After(now) && now.Sub(v.VerifiedAt) <= ActivationReceiptMaxAge
}

// EndpointPin binds the canonical TLS endpoint to the physical native
// target. The relay and managed-source lookup use the same canonicalization.
func EndpointPin(target, host string, port int) string {
	if addr, err := netip.ParseAddr(host); err == nil {
		host = addr.String()
	} else {
		host = strings.ToLower(host)
	}
	hash := sha256.Sum256([]byte(target + "\n" + net.JoinHostPort(host, strconv.Itoa(port))))
	return hex.EncodeToString(hash[:])
}
