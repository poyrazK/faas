// Package referrals owns acquisition attribution and reward estimates. It has
// no payment-provider authority: estimates are never credits or payouts.
package referrals

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"
)

var ErrNotFound = errors.New("referral not found")
var codePattern = regexp.MustCompile(`^[A-Z0-9]{6}$`)

func NormalizeCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

func ValidCode(code string) bool { return codePattern.MatchString(code) }

func NewCode() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var code [6]byte
	for i := 0; i < len(code); {
		var random [16]byte
		_, _ = rand.Read(random[:])
		for _, value := range random {
			if value >= 252 {
				continue
			} // reject the uneven tail before reducing modulo 36
			code[i] = alphabet[int(value)%len(alphabet)]
			i++
			if i == len(code) {
				break
			}
		}
	}
	return string(code[:])
}

// The original identifier remains private so historical attribution FKs never
// change when a short, shareable code is assigned to an existing source.
func newLinkID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Policy is copied into each new attribution. Reconfiguring the program never
// rewrites existing terms. All money uses integer cents, in one currency.
type Policy struct {
	Currency            string `json:"currency"`
	CustomerRewardCents int64  `json:"customer_reward_cents"`
	ReferredRewardCents int64  `json:"referred_reward_cents"`
	CommissionBPS       int32  `json:"commission_bps"`
	HoldDays            int32  `json:"hold_days"`
	ConversionDays      int32  `json:"conversion_days"`
}

type Attribution struct {
	Code   string
	Policy Policy
}

type Link struct {
	Code      string    `json:"code"`
	OwnerID   string    `json:"-"`
	Kind      string    `json:"kind"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

type Scope struct {
	OwnerID string
	All     bool
	Kind    string
}

type LeadUpdate struct {
	Stage    string `json:"stage"`
	Note     string `json:"note"`
	FollowUp string `json:"follow_up"`
}

type Record struct {
	AccountID           string     `json:"account_id"`
	OwnerID             string     `json:"owner_id,omitempty"`
	Kind                string     `json:"kind"`
	Email               string     `json:"email,omitempty"`
	Plan                string     `json:"plan,omitempty"`
	AccountStatus       string     `json:"account_status,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	Stage               string     `json:"stage,omitempty"`
	Note                string     `json:"note,omitempty"`
	FollowUp            string     `json:"follow_up,omitempty"`
	Apps                int64      `json:"apps,omitempty"`
	Paid                bool       `json:"paid"`
	RewardStatus        string     `json:"reward_status"`
	Currency            string     `json:"currency"`
	RewardCents         int64      `json:"reward_cents"`
	ReferredRewardCents int64      `json:"referred_reward_cents"`
	PaidCents           int64      `json:"paid_cents"`
	EligibleAt          *time.Time `json:"eligible_at,omitempty"`
}

type Summary struct {
	Kind        string `json:"kind"`
	Currency    string `json:"currency"`
	Signups     int64  `json:"signups"`
	Paid        int64  `json:"paid"`
	Eligible    int64  `json:"eligible"`
	RewardCents int64  `json:"reward_cents"`
	PaidCents   int64  `json:"paid_cents"`
}

type Store interface {
	EnsureLink(context.Context, string, string) (Link, error)
	Link(context.Context, string) (Link, error)
	OwnLink(context.Context, string, string) (Link, error)
	SetLinkActive(context.Context, Scope, string, bool) (bool, error)
	List(context.Context, Scope, string, int32) ([]Record, error)
	Summary(context.Context, Scope) ([]Summary, error)
	UpdateLead(context.Context, Scope, string, string, LeadUpdate) (bool, error)
}
