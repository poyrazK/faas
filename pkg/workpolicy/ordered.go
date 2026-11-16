package workpolicy

import (
	"fmt"
	"strings"
)

// Ordered is the built-in policy behind a queue message's group_key
// (ADR-20261112193306): one running message per key, every pending message
// kept in admission order, a retrying message blocking later ones of its key.
// Every app has it; the store creates its row on first use.
const Ordered = "gregale-ordered"

// ReservedPrefix marks policy names Gregale owns.
const ReservedPrefix = "gregale-"

// OrderedPolicy is the built-in ordered policy.
func OrderedPolicy() Policy {
	return Policy{Name: Ordered, MaxRunningPerKey: 1, PendingUpdates: PendingAll}
}

// IsOrdered reports whether p has the built-in ordered policy's settings.
func IsOrdered(p Policy) bool {
	pending := p.PendingUpdates
	if pending == "" {
		pending = PendingAll
	}
	return p.Name == Ordered && p.MaxRunningPerKey == 1 && pending == PendingAll &&
		p.Debounce == 0 && p.ExpiresAfter == 0 && p.MaxRunningPerFairnessKey == 0
}

// ValidateDeclaredName refuses a customer-declared policy name Gregale
// reserves.
func ValidateDeclaredName(name string) error {
	if strings.HasPrefix(name, ReservedPrefix) {
		return fmt.Errorf("work policy names starting with %q are reserved", ReservedPrefix)
	}
	return nil
}
