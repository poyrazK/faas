// Package sales serves Gregale's staff portal and customer referral program.
package sales

import (
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
)

type Config struct {
	StaleDays          int32
	IntakeEnabled      bool
	IntakeFollowupDays int
	IntakeTimezone     *time.Location
	Enabled            bool
	Members            map[string]bool // value true grants team-wide management visibility
	Policy             referrals.Policy
	PublicOrigin       string
}

func (c Config) Member(email string) (manager, member bool) {
	manager, member = c.Members[strings.ToLower(strings.TrimSpace(email))]
	return
}

func FromEnv(getenv func(string) string, origin string) (Config, error) {
	c := Config{Enabled: getenv("FAAS_SALES_ENABLED") == "1", PublicOrigin: origin, Members: map[string]bool{}, Policy: referrals.Policy{Currency: "usd", HoldDays: 30, ConversionDays: 90}}
	c.IntakeEnabled = getenv("FAAS_SALES_INTAKE_ENABLED") == "1"
	c.IntakeFollowupDays = api.SalesIntakeFollowupDefaultDays
	c.IntakeTimezone = time.UTC
	c.StaleDays = api.SalesStaleDefaultDays
	if !c.Enabled {
		return c, nil
	}
	if value := getenv("FAAS_SALES_STALE_DAYS"); value != "" {
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil || n < 1 || n > api.SalesStaleMaxDays {
			return Config{}, fmt.Errorf("FAAS_SALES_STALE_DAYS must be between 1 and %d", api.SalesStaleMaxDays)
		}
		c.StaleDays = int32(n)
	}
	if value := getenv("FAAS_SALES_INTAKE_FOLLOWUP_DAYS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > api.SalesIntakeFollowupMaxDays {
			return Config{}, fmt.Errorf("FAAS_SALES_INTAKE_FOLLOWUP_DAYS must be between 0 and %d", api.SalesIntakeFollowupMaxDays)
		}
		c.IntakeFollowupDays = n
	}
	if value := getenv("FAAS_SALES_INTAKE_TIMEZONE"); value != "" {
		zone, err := time.LoadLocation(value)
		if err != nil {
			return Config{}, fmt.Errorf("FAAS_SALES_INTAKE_TIMEZONE must be an IANA time zone")
		}
		c.IntakeTimezone = zone
	}
	for _, group := range []struct {
		key     string
		manager bool
	}{{"FAAS_SALES_EMAILS", false}, {"FAAS_SALES_MANAGER_EMAILS", true}} {
		for _, email := range strings.Split(getenv(group.key), ",") {
			email = strings.ToLower(strings.TrimSpace(email))
			if email == "" {
				continue
			}
			parsed, err := mail.ParseAddress(email)
			if err != nil || parsed.Address != email {
				return Config{}, fmt.Errorf("%s contains an invalid email address", group.key)
			}
			c.Members[email] = group.manager || c.Members[email]
		}
	}
	if value := getenv("FAAS_REFERRAL_CURRENCY"); value != "" {
		c.Policy.Currency = value
	}
	if c.Policy.Currency != "usd" && c.Policy.Currency != "eur" {
		return Config{}, fmt.Errorf("FAAS_REFERRAL_CURRENCY must be usd or eur")
	}
	fields := []struct {
		key string
		max int64
		set func(int64)
	}{
		{"FAAS_REFERRAL_REWARD_CENTS", api.ReferralRewardMaxCents, func(v int64) { c.Policy.CustomerRewardCents = v }},
		{"FAAS_REFERRAL_NEW_CUSTOMER_REWARD_CENTS", api.ReferralRewardMaxCents, func(v int64) { c.Policy.ReferredRewardCents = v }},
		{"FAAS_SALES_COMMISSION_BPS", api.ReferralCommissionMaxBPS, func(v int64) { c.Policy.CommissionBPS = int32(v) }},
		{"FAAS_REFERRAL_HOLD_DAYS", api.ReferralPolicyMaxDays, func(v int64) { c.Policy.HoldDays = int32(v) }},
		{"FAAS_REFERRAL_CONVERSION_DAYS", api.ReferralPolicyMaxDays, func(v int64) { c.Policy.ConversionDays = int32(v) }},
	}
	for _, field := range fields {
		value := getenv(field.key)
		if value == "" {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 || n > field.max {
			return Config{}, fmt.Errorf("%s must be an integer between 0 and %d", field.key, field.max)
		}
		field.set(n)
	}
	if c.Policy.ConversionDays == 0 {
		return Config{}, fmt.Errorf("FAAS_REFERRAL_CONVERSION_DAYS must be positive")
	}
	return c, nil
}
