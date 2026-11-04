package netns

// Public entry block list (ADR-20261103163954 §2).
//
// The operator blocks address ranges on a node's public entry addresses by
// listing them, each with an expiry, in a file vmmd reads. The rendered
// ruleset only declares the sets (public_block_v4/v6, interval sets with
// per-element timeouts) and drops their sources ahead of everything else on
// the entry addresses: elements whose remaining time changes every second
// would make every render differ. vmmd adds the elements after each reload,
// which flushes the ruleset, and again on a short loop, so an edit takes
// effect within that loop and an entry survives every reload.

import (
	"bufio"
	"bytes"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// PublicBlock is one live block-list entry: a source range and the time left
// before it lapses.
type PublicBlock struct {
	Prefix  netip.Prefix
	Timeout time.Duration
}

// publicBlockTimeoutMax caps one entry's remaining time, so a typo in the
// year cannot block a range for good.
const publicBlockTimeoutMax = 90 * 24 * time.Hour

// ParsePublicBlockList reads the operator's block list at now. Each line is
// "<prefix> <expiry RFC 3339> [reason...]"; blank lines and lines starting
// with '#' are skipped. A prefix must be in canonical form (192.0.2.0/24,
// 198.51.100.7/32, 2001:db8::/48). Lapsed entries are left out. A malformed
// line is skipped and reported in errs, so one typo does not drop the rest.
func ParsePublicBlockList(data []byte, now time.Time) (entries []PublicBlock, errs []error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			errs = append(errs, fmt.Errorf("line %d: want \"<prefix> <expiry RFC 3339> [reason]\"", n))
			continue
		}
		prefix, err := netip.ParsePrefix(fields[0])
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" {
			errs = append(errs, fmt.Errorf("line %d: %q is not a canonical network prefix", n, fields[0]))
			continue
		}
		expires, err := time.Parse(time.RFC3339, fields[1])
		if err != nil {
			errs = append(errs, fmt.Errorf("line %d: expiry %q is not RFC 3339", n, fields[1]))
			continue
		}
		left := expires.Sub(now)
		if left <= 0 {
			continue
		}
		entries = append(entries, PublicBlock{Prefix: prefix, Timeout: min(left, publicBlockTimeoutMax)})
	}
	if err := scanner.Err(); err != nil {
		errs = append(errs, fmt.Errorf("read block list: %w", err))
	}
	return entries, errs
}

// PublicBlockScript is the nft script that adds entries to the block sets of
// the families entryAddrs declares; entries of another family are left out,
// as are all of them when there is no entry address. A timeout is rendered
// in whole seconds, rounded up. Empty when nothing applies.
func PublicBlockScript(entries []PublicBlock, entryAddrs []netip.Addr) string {
	families := publicEntryFamilies(entryAddrs)
	var b strings.Builder
	for _, e := range entries {
		family := "ip"
		if e.Prefix.Addr().Is6() {
			family = "ip6"
		}
		declared := false
		for _, f := range families {
			declared = declared || f == family
		}
		if !declared {
			continue
		}
		seconds := int64((e.Timeout + time.Second - 1) / time.Second)
		fmt.Fprintf(&b, "add element inet faas public_block_%s { %s timeout %ds }\n", publicEntrySetSuffix(family), e.Prefix, seconds)
	}
	return b.String()
}
