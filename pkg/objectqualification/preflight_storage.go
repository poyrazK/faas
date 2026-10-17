package objectqualification

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// StorageObservation describes one mount in this kernel's namespace. Device
// numbers and mount IDs are local identifiers, not physical failure domains.
type StorageObservation struct {
	Parent              string `json:"parent"`
	MountID             uint64 `json:"mount_id"`
	MountPoint          string `json:"mount_point"`
	MountRoot           string `json:"mount_root"`
	Device              string `json:"device_major_minor"`
	RootDevice          string `json:"root_device_major_minor"`
	Filesystem          string `json:"filesystem"`
	FilesystemType      int64  `json:"filesystem_type"`
	LocalBlockDevice    bool   `json:"local_block_device"`
	ReadOnly            bool   `json:"read_only"`
	DirectoryAccessible bool   `json:"directory_accessible"`
	AvailableBytes      uint64 `json:"available_bytes"`
	AvailableInodes     uint64 `json:"available_inodes"`
}

func inspectStorage(directory, role string) (StorageObservation, error) {
	s := StorageObservation{Parent: filepath.Dir(directory)}
	if err := freshInspectionPaths(directory, role); err != nil {
		return s, err
	}
	canonical, err := filepath.EvalSymlinks(s.Parent)
	if err != nil || canonical != s.Parent {
		return s, errors.New("qualification parent must exist without symlink components")
	}
	var identity unix.Statx_t
	if err = unix.Statx(unix.AT_FDCWD, s.Parent, unix.AT_NO_AUTOMOUNT|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &identity); err != nil || identity.Mask&unix.STATX_MNT_ID == 0 {
		return s, errors.New("qualification mount identity is unavailable")
	}
	var stat unix.Statfs_t
	if err = unix.Statfs(s.Parent, &stat); err != nil {
		return s, err
	}
	if stat.Bsize <= 0 || stat.Bavail > ^uint64(0)/uint64(stat.Bsize) {
		return s, errors.New("invalid qualification filesystem capacity")
	}
	s.MountID, s.FilesystemType = identity.Mnt_id, int64(stat.Type)
	s.AvailableBytes, s.AvailableInodes = stat.Bavail*uint64(stat.Bsize), stat.Ffree
	s.Device = strconv.FormatUint(uint64(identity.Dev_major), 10) + ":" + strconv.FormatUint(uint64(identity.Dev_minor), 10)
	var rootIdentity unix.Statx_t
	if err = unix.Statx(unix.AT_FDCWD, "/", unix.AT_NO_AUTOMOUNT, unix.STATX_BASIC_STATS, &rootIdentity); err != nil {
		return s, err
	}
	s.RootDevice = strconv.FormatUint(uint64(rootIdentity.Dev_major), 10) + ":" + strconv.FormatUint(uint64(rootIdentity.Dev_minor), 10)
	s.ReadOnly = stat.Flags&unix.ST_RDONLY != 0
	s.DirectoryAccessible = unix.Access(s.Parent, unix.W_OK|unix.X_OK) == nil
	file, err := os.Open("/proc/self/mountinfo") //nolint:forbidigo // Fixed kernel mount metadata, never a customer path.
	if err != nil {
		return s, err
	}
	defer func() { _ = file.Close() }()
	data, err := readInspectionMetadata(file)
	if err != nil {
		return s, err
	}
	mount, err := findInspectionMount(data, s.MountID)
	if err != nil {
		return s, err
	}
	if mount.Device != s.Device || !pathOnMount(s.Parent, mount.MountPoint) {
		return s, errors.New("qualification mount observation changed")
	}
	s.MountPoint, s.MountRoot, s.Filesystem = mount.MountPoint, mount.MountRoot, mount.Filesystem
	s.ReadOnly = s.ReadOnly || mount.ReadOnly
	device, err := os.Stat(filepath.Join("/sys/dev/block", s.Device))
	s.LocalBlockDevice = err == nil && device.IsDir()
	return s, nil
}

func freshInspectionPaths(directory, role string) error {
	paths := []string{directory}
	if role == "follower" {
		paths = append(paths, directory+"-local", directory+"-replica")
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("qualification work paths must not exist and must be accessible")
		}
	}
	parent, err := os.Stat(filepath.Dir(directory))
	if err != nil || !parent.IsDir() {
		return errors.New("qualification work parent must be an existing directory")
	}
	return nil
}

func findInspectionMount(data []byte, id uint64) (StorageObservation, error) {
	var found StorageObservation
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, api.ObjectQualificationAgentChunkBytes), api.ObjectQualificationAgentLineBytes)
	for scanner.Scan() {
		left, right, ok := strings.Cut(scanner.Text(), " - ")
		fields, tail := strings.Fields(left), strings.Fields(right)
		if !ok || len(fields) < 6 || len(tail) < 3 {
			return found, errors.New("invalid qualification mount metadata")
		}
		current, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return found, errors.New("invalid qualification mount identity")
		}
		if current != id {
			continue
		}
		if found.MountID != 0 {
			return found, errors.New("duplicate qualification mount identity")
		}
		root, err := unescapeMountPath(fields[3])
		if err != nil {
			return found, err
		}
		point, err := unescapeMountPath(fields[4])
		if err != nil {
			return found, err
		}
		found = StorageObservation{MountID: id, MountRoot: root, MountPoint: point, Device: fields[2], Filesystem: tail[0], ReadOnly: mountReadOnly(fields[5]) || mountReadOnly(tail[2])}
	}
	if scanner.Err() != nil {
		return found, scanner.Err()
	}
	if found.MountID == 0 {
		return found, errors.New("qualification mount identity not found")
	}
	return found, nil
}

func unescapeMountPath(value string) (string, error) {
	var result strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			result.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", errors.New("invalid mount path escape")
		}
		switch value[i+1 : i+4] {
		case "040":
			result.WriteByte(' ')
		case "011":
			result.WriteByte('\t')
		case "012":
			result.WriteByte('\n')
		case "134":
			result.WriteByte('\\')
		default:
			return "", errors.New("invalid mount path escape")
		}
		i += 3
	}
	path := result.String()
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("invalid mount path")
	}
	return path, nil
}

func mountReadOnly(options string) bool {
	for _, option := range strings.Split(options, ",") {
		if option == "ro" {
			return true
		}
	}
	return false
}

func pathOnMount(path, mount string) bool {
	return path == mount || mount == "/" && filepath.IsAbs(path) || strings.HasPrefix(path, mount+"/")
}

func validateInspectionStorage(s StorageObservation) error {
	var problems []error
	if s.MountID == 0 || s.MountPoint == "/" || s.MountPoint == "" || s.MountRoot != "/" || s.Device == "" || s.RootDevice == "" || s.Device == s.RootDevice || !pathOnMount(s.Parent, s.MountPoint) {
		problems = append(problems, errors.New("a dedicated filesystem mount outside the root filesystem is required"))
	}
	if s.Filesystem != "ext4" && s.Filesystem != "xfs" || s.Filesystem == "ext4" && s.FilesystemType != unix.EXT4_SUPER_MAGIC || s.Filesystem == "xfs" && s.FilesystemType != unix.XFS_SUPER_MAGIC || !s.LocalBlockDevice {
		problems = append(problems, errors.New("qualification requires local block-backed ext4 or XFS storage"))
	}
	if s.ReadOnly || !s.DirectoryAccessible {
		problems = append(problems, errors.New("qualification parent must permit directory traversal and writes on a writable mount"))
	}
	minimum := uint64(api.ObjectQualificationDiskBudget + api.ObjectUploadSpoolMinFreeBytes + api.ObjectNodeAdmissionFreeRecovery)
	if s.AvailableBytes < minimum {
		problems = append(problems, fmt.Errorf("qualification needs at least %d available bytes; observed %d", minimum, s.AvailableBytes))
	}
	if s.AvailableInodes == 0 {
		problems = append(problems, errors.New("qualification requires available filesystem inodes"))
	}
	return errors.Join(problems...)
}

// Keep procfs input bounded before allocating it; its budget is the existing
// qualification metadata/report bound, independent of the selected filesystem.
func readInspectionMetadata(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, int64(api.ObjectQualificationReportBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > api.ObjectQualificationReportBytes {
		return nil, errors.New("qualification metadata exceeds its report budget")
	}
	return data, nil
}
