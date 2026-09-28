package workpolicy

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"
)

// DigestKey hashes a canonical key before it enters the invocation ledger.
// The policy and app remain separate lane identity components.
func DigestKey(key string) ([32]byte, error) {
	if len(key) < 3 || len(key) > MaxKeyBytes {
		return [32]byte{}, fmt.Errorf("work key must be a bounded canonical scalar")
	}
	switch {
	case strings.HasPrefix(key, "s:"):
	case strings.HasPrefix(key, "n:"):
		value, ok := new(big.Rat).SetString(strings.TrimPrefix(key, "n:"))
		if !ok || value.RatString() != strings.TrimPrefix(key, "n:") {
			return [32]byte{}, fmt.Errorf("work key number is not canonical")
		}
	case key == "b:true" || key == "b:false":
	default:
		return [32]byte{}, fmt.Errorf("work key must be a canonical scalar")
	}
	return sha256.Sum256([]byte(key)), nil
}
