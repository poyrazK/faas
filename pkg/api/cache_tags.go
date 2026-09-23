package api

import (
	"fmt"
	"sort"
	"strings"
)

const (
	CacheTagMaxBytes       = 128
	CacheTagHeaderMaxBytes = 2048
	CacheTagMaxCount       = 32
)

// NormalizeCacheTag validates a single tag used for a targeted cache purge.
// Tags are case-insensitive ASCII identifiers; separators allow names such as
// product:42 while keeping commas and whitespace reserved for header parsing.
func NormalizeCacheTag(raw string) (string, error) {
	if raw == "" || len(raw) > CacheTagMaxBytes {
		return "", fmt.Errorf("cache tag must be 1–%d bytes", CacheTagMaxBytes)
	}
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.' || b == ':' || b == '/') {
			return "", fmt.Errorf("cache tag contains an invalid character")
		}
	}
	return strings.ToLower(raw), nil
}

// ParseCacheTags parses origin Cache-Tag headers into bounded, canonical
// metadata. Invalid metadata must prevent storage, not produce an untagged
// entry that a later tag purge cannot remove.
func ParseCacheTags(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{})
	total := 0
	for _, value := range values {
		total += len(value)
		if total > CacheTagHeaderMaxBytes {
			return nil, fmt.Errorf("cache tag header is too long")
		}
		for _, part := range strings.Split(value, ",") {
			tag, err := NormalizeCacheTag(strings.TrimSpace(part))
			if err != nil {
				return nil, err
			}
			seen[tag] = struct{}{}
			if len(seen) > CacheTagMaxCount {
				return nil, fmt.Errorf("too many cache tags")
			}
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags, nil
}
