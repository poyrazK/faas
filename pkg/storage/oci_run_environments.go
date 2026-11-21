package storage

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/runfile"
	"strings"
)

const repoRunEnvironments = "run-environments"
const repoRunEnvironmentScans = "run-environment-scans"

// A fixed repository makes cleanup enumerable after restart without registry
// catalog support. Compact UUIDs keep every reversible tag below OCI's 128-byte
// bound; the owner prefix preserves both canonical UUID and legacy hex owners.
func planRunEnvironment(key string) (string, string, error) {
	repo := repoRunEnvironments
	value := key
	if strings.HasPrefix(value, "scans/") {
		repo = repoRunEnvironmentScans
		value = strings.TrimPrefix(value, "scans/")
	}
	p := strings.Split(value, "/")
	if len(p) != 5 || p[0] != repoRunEnvironments || !runfile.ValidOwnerID(p[1]) || !runenv.ValidID(p[2]) || !runenv.ValidID(p[3]) {
		return "", "", fmt.Errorf("%w: invalid environment key", ErrInvalidKey)
	}
	if repo == repoRunEnvironmentScans {
		if p[4] != "base.ext4.scan.json" {
			return "", "", ErrInvalidKey
		}
	} else {
		switch p[4] {
		case "image.tar", "base.ext4", "layer.ext4", "base.ext4.digest", "base.ext4.content":
		default:
			return "", "", ErrInvalidKey
		}
	}
	ownerKind := "h"
	if len(p[1]) == 36 {
		ownerKind = "u"
	}
	tag := ownerKind + strings.ReplaceAll(p[1], "-", "") + "__" + strings.ReplaceAll(p[2], "-", "") + "__" + strings.ReplaceAll(p[3], "-", "") + "__" + p[4]
	if !tagCharset.MatchString(tag) {
		return "", "", ErrInvalidKey
	}
	return repo, tag, nil
}

func unplanRunEnvironment(repo, tag string) (string, bool) {
	p := strings.Split(tag, "__")
	if len(p) != 4 || len(p[0]) != 33 {
		return "", false
	}
	parse := func(value string) string {
		id, err := uuid.Parse(value)
		if err != nil || len(value) != 32 || strings.ReplaceAll(id.String(), "-", "") != value {
			return ""
		}
		return id.String()
	}
	owner := p[0][1:]
	switch p[0][0] {
	case 'u':
		owner = parse(owner)
	case 'h':
	default:
		return "", false
	}
	id, lease := parse(p[1]), parse(p[2])
	if owner == "" || id == "" || lease == "" {
		return "", false
	}
	key := repoRunEnvironments + "/" + owner + "/" + id + "/" + lease + "/" + p[3]
	if repo == repoRunEnvironmentScans {
		key = "scans/" + key
	}
	planned, encoded, err := planRunEnvironment(key)
	return key, err == nil && planned == repo && encoded == tag
}
