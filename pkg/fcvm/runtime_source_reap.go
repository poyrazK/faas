package fcvm

// adr: 435. A surviving Firecracker process may still hold these sealed files.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
)

type RuntimeSourceReapOptions struct {
	Root   string
	IsLive LiveInstanceFunc
	MinAge time.Duration
	now    func() time.Time
}

type RuntimeSourceReapReport struct {
	Scanned, Reaped, SkippedActive, SkippedLive, SkippedYoung, SkippedUnknown, Failed int
	ReclaimedLogicalBytes                                                             int64
}

// ReapOrphanedRuntimeSources removes only recognized, unlocked process roots
// whose complete owner set is durably dead. Unknown records or database errors
// preserve the entire shared root. Old unjournaled /tmp files are not guessed at.
func ReapOrphanedRuntimeSources(ctx context.Context, opts RuntimeSourceReapOptions) (RuntimeSourceReapReport, error) {
	var rep RuntimeSourceReapReport
	if opts.Root == "" || !filepath.IsAbs(opts.Root) || opts.IsLive == nil {
		return rep, errors.New("fcvm: runtime source reap requires an absolute root and durable liveness gate")
	}
	if opts.MinAge <= 0 {
		opts.MinAge = DefaultReapMinAge
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	entries, err := readRuntimeSourceRoots(opts.Root)
	if os.IsNotExist(err) {
		return rep, nil
	}
	if err != nil {
		return rep, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), runtimeSourceRootPrefix) {
			continue
		}
		rep.Scanned++
		if err := reapRuntimeSourceRoot(ctx, filepath.Join(opts.Root, entry.Name()), opts, &rep); err != nil {
			rep.Failed++
		}
	}
	return rep, ctx.Err()
}

func readRuntimeSourceRoots(root string) ([]os.DirEntry, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !runtimeSourceOwned(info) {
		return nil, errors.New("fcvm: runtime source reap parent must be private and owned")
	}
	return os.ReadDir(root)
}

func reapRuntimeSourceRoot(ctx context.Context, path string, opts RuntimeSourceReapOptions, rep *RuntimeSourceReapReport) (err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !runtimeSourceOwned(info) {
		rep.SkippedUnknown++
		return nil
	}
	if opts.now().Sub(info.ModTime()) < opts.MinAge {
		rep.SkippedYoung++
		return nil
	}
	if !regularRuntimeSourceFile(filepath.Join(path, ".lock")) {
		rep.SkippedUnknown++
		return nil
	}
	lock := flock.New(filepath.Join(path, ".lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		rep.SkippedActive++
		return nil
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owners, size, err := runtimeSourceOwners(path)
	if err != nil {
		rep.SkippedUnknown++
		return nil
	}
	if !runtimeSourceOwnersDead(ctx, owners, opts.IsLive, rep) {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	rep.Reaped++
	rep.ReclaimedLogicalBytes += size
	return nil
}

func regularRuntimeSourceFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && runtimeSourceOwned(info)
}

func runtimeSourceOwners(path string) ([]string, int64, error) {
	if !regularRuntimeSourceFile(filepath.Join(path, ".format")) {
		return nil, 0, errors.New("missing runtime source format")
	}
	format, err := readRuntimeSourceRecord(filepath.Join(path, ".format"))
	if err != nil || string(format) != runtimeSourceFormat {
		return nil, 0, errors.New("unknown runtime source format")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, 0, err
	}
	var owners []string
	var size int64
	for _, entry := range entries {
		file := filepath.Join(path, entry.Name())
		if !regularRuntimeSourceFile(file) {
			return nil, 0, errors.New("nonregular runtime source entry")
		}
		switch name := entry.Name(); {
		case name == ".format" || name == ".lock", strings.HasPrefix(name, "owner-write-"):
		case strings.HasPrefix(name, "faas-snap-runtime-"):
			info, err := os.Lstat(file)
			if err != nil {
				return nil, 0, err
			}
			size += info.Size()
		default:
			owner, err := readRuntimeSourceOwner(file)
			if err != nil || name != runtimeSourceOwnerName(owner.InstanceID) {
				return nil, 0, errors.New("unknown runtime source owner")
			}
			owners = append(owners, owner.InstanceID)
		}
	}
	return owners, size, nil
}

func readRuntimeSourceRecord(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(file, runtimeSourceRecordMaxBytes+1))
	err = errors.Join(err, file.Close())
	if len(body) > runtimeSourceRecordMaxBytes {
		err = errors.Join(err, errors.New("oversized runtime source record"))
	}
	return body, err
}

func readRuntimeSourceOwner(path string) (runtimeSourceOwner, error) {
	var owner runtimeSourceOwner
	body, err := readRuntimeSourceRecord(path)
	if err != nil {
		return owner, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&owner); err != nil {
		return owner, err
	}
	id, err := uuid.Parse(owner.InstanceID)
	if err != nil || id == uuid.Nil || id.String() != owner.InstanceID || owner.Version != 1 {
		return owner, errors.New("invalid runtime source owner identity")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return owner, errors.New("trailing runtime source record")
	}
	canonical, err := json.Marshal(owner)
	if err != nil || !bytes.Equal(canonical, body) {
		return owner, errors.New("noncanonical runtime source owner record")
	}
	return owner, nil
}

func runtimeSourceOwnersDead(ctx context.Context, owners []string, isLive LiveInstanceFunc, rep *RuntimeSourceReapReport) bool {
	for _, owner := range owners {
		if ctx.Err() != nil {
			return false
		}
		live, err := isLive(ctx, owner)
		if err != nil {
			rep.SkippedUnknown++
			return false
		}
		if live {
			rep.SkippedLive++
			return false
		}
	}
	return ctx.Err() == nil
}
