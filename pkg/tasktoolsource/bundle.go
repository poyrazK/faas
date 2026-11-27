package tasktoolsource

import (
	"crypto/sha256"
	"encoding/hex"

	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Digest uses the existing hosted review's lexical depth-first path/mode/byte
// framing. Only normalized files and explicit directories can be retained.
func Digest(entries []api.HostedTaskSourceEntry) (string, error) {
	if len(entries) == 0 || len(entries) > api.HostedTaskSourceRoleMaxEntries {
		return "", ErrInvalid
	}
	paths := map[string]api.HostedTaskSourceEntry{}
	total := 0
	for _, e := range entries {
		if e.Path != "." && (!fs.ValidPath(e.Path) || strings.ContainsAny(e.Path, "\\\x00")) || e.Path == "." && !e.Directory || e.Mode != 0600 && e.Mode != 0700 || e.Directory && (e.Mode != 0700 || len(e.Content) != 0) {
			return "", ErrInvalid
		}
		for _, part := range strings.Split(e.Path, "/") {
			if strings.EqualFold(part, ".git") || strings.EqualFold(part, ".gregale") {
				return "", ErrInvalid
			}
		}
		if _, exists := paths[e.Path]; exists {
			return "", ErrInvalid
		}
		paths[e.Path] = e
		total += len(e.Content)
		if total > api.HostedTaskSourceRoleMaxBytes {
			return "", ErrInvalid
		}
	}
	if !paths["."].Directory {
		return "", ErrInvalid
	}
	for _, e := range entries {
		if e.Path != "." {
			parent := "."
			if i := strings.LastIndex(e.Path, "/"); i >= 0 {
				parent = e.Path[:i]
			}
			if !paths[parent].Directory {
				return "", ErrInvalid
			}
		}
	}
	ordered := orderedEntries(entries)
	h := sha256.New()
	for _, e := range ordered {
		if e.Directory {
			_, _ = fmt.Fprintf(h, "directory:%s\x00%o\x00", e.Path, e.Mode)
		} else {
			_, _ = fmt.Fprintf(h, "%s\x00%d\x00%o\x00", e.Path, len(e.Content), e.Mode)
			_, _ = h.Write(e.Content)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func orderedEntries(entries []api.HostedTaskSourceEntry) []api.HostedTaskSourceEntry {
	ordered := slices.Clone(entries)
	slices.SortFunc(ordered, func(a, b api.HostedTaskSourceEntry) int {
		if a.Path == b.Path {
			return 0
		}
		if a.Path == "." {
			return -1
		}
		if b.Path == "." {
			return 1
		}
		return slices.Compare(strings.Split(a.Path, "/"), strings.Split(b.Path, "/"))
	})
	return ordered
}
