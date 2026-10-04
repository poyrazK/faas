// Package ociidentity resolves OCI process identity within an image filesystem.
package ociidentity

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Identity is the primary UID/GID selected for one image process.
type Identity struct{ UID, GID uint32 }

// ValidateSpec checks identity syntax without reading image layers. Empty is
// valid OCI metadata and lets guest-init apply its normal default.
func ValidateSpec(spec string) error {
	if spec == "" {
		return nil
	}
	user, group, explicit := strings.Cut(spec, ":")
	if user == "" || strings.ContainsAny(spec, "\x00\r\n\t ") || (explicit && (group == "" || strings.Contains(group, ":"))) {
		return fmt.Errorf("invalid OCI user/group specification")
	}
	if _, _, err := parseID(user); err != nil {
		return err
	}
	if explicit {
		if _, _, err := parseID(group); err != nil {
			return err
		}
	}
	return nil
}

// Resolve retains the legacy fallback for a missing implicit named user. An
// explicit group must resolve; silently substituting another group can grant
// the wrong filesystem access. The caller supplies an image-root-confined FS.
func Resolve(image fs.FS, spec string, fallback Identity) (Identity, error) {
	if err := ValidateSpec(spec); err != nil {
		return Identity{}, err
	}
	user, group, explicit := strings.Cut(spec, ":")
	if user == "" {
		return fallback, nil
	}
	uid, numeric, err := parseID(user)
	if err != nil {
		return Identity{}, fmt.Errorf("user identity: %w", err)
	}
	result := fallback
	if numeric {
		result = Identity{UID: uid, GID: uid}
	}
	passwd, err := readIdentityFile(image, "etc/passwd")
	if err != nil {
		return Identity{}, fmt.Errorf("read image passwd: %w", err)
	}
	found := false
	for _, line := range strings.Split(passwd, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 4 || strings.HasPrefix(line, "#") {
			continue
		}
		entryUID, ok, idErr := parseID(fields[2])
		if (numeric && (!ok || idErr != nil || entryUID != uid)) || (!numeric && fields[0] != user) {
			continue
		}
		entryGID, gidOK, gidErr := parseID(fields[3])
		if !ok || idErr != nil || !gidOK || gidErr != nil {
			return Identity{}, fmt.Errorf("invalid image passwd identity")
		}
		result, found = Identity{UID: entryUID, GID: entryGID}, true
		break
	}
	if explicit && !numeric && !found && user != api.DefaultAppUser {
		return Identity{}, fmt.Errorf("explicit OCI user is absent from image passwd")
	}
	if !explicit {
		return result, nil
	}
	gid, numericGroup, err := parseID(group)
	if err != nil {
		return Identity{}, fmt.Errorf("group identity: %w", err)
	}
	if numericGroup {
		result.GID = gid
		return result, nil
	}
	groups, err := readIdentityFile(image, "etc/group")
	if err != nil {
		return Identity{}, fmt.Errorf("read image group: %w", err)
	}
	for _, line := range strings.Split(groups, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 3 || strings.HasPrefix(line, "#") || fields[0] != group {
			continue
		}
		gid, ok, err := parseID(fields[2])
		if !ok || err != nil {
			return Identity{}, fmt.Errorf("invalid image group identity")
		}
		result.GID = gid
		return result, nil
	}
	return Identity{}, fmt.Errorf("explicit OCI group is absent from image group")
}

func parseID(value string) (uint32, bool, error) {
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, true, fmt.Errorf("identity must be unsigned")
	}
	if value == "" {
		return 0, false, nil
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false, nil
		}
	}
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil || id > api.OCIIdentityIDMax {
		return 0, true, fmt.Errorf("identity exceeds supported UID/GID range")
	}
	return uint32(id), true, nil
}

func readIdentityFile(image fs.FS, name string) (string, error) {
	file, err := image.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.OCIIdentityFileMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > api.OCIIdentityFileMaxBytes {
		return "", fmt.Errorf("image identity file exceeds supported size")
	}
	return string(body), nil
}
