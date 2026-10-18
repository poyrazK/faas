package objectnode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
	"golang.org/x/sys/unix"
)

// Usage audits the current catalog and samples filesystem consumption without
// recovery or cleanup. A bad reference is evidence of damage, never zero usage.
func (s *Store) Usage(ctx context.Context) (objectinventory.Usage, error) {
	return s.usage(ctx, true)
}

func (s *Store) usage(ctx context.Context, counters bool) (objectinventory.Usage, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return objectinventory.Usage{}, ErrClosed
	}
	u := objectinventory.Usage{Version: objectinventory.Version, Snapshot: s.inventorySnapshot(), StartedAt: time.Now().UTC(), CatalogHealthy: true, PhysicalSample: true, Issues: []objectinventory.Issue{}}
	var format struct {
		Engine  string `json:"engine"`
		Version int    `json:"version"`
	}
	if readJSON(filepath.Join(s.root, "format.json"), &format) != nil || format.Engine != "gregale-object-node" || format.Version < 3 || format.Version > 5 {
		addInventoryIssue(&u, "invalid_format_marker", "format.json", true)
	}
	if s.pending != nil || s.replicationDirty {
		addInventoryIssue(&u, "pending_receipt", "receipts", true)
	}
	checkCounters := counters && s.catalogIndex != nil
	if err := s.auditBuckets(ctx, &u, checkCounters); err != nil {
		return objectinventory.Usage{}, err
	}
	if err := s.auditUploads(ctx, &u, checkCounters); err != nil {
		return objectinventory.Usage{}, err
	}
	if err := s.auditFiles(ctx, &u, "", 0); err != nil {
		return objectinventory.Usage{}, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(s.root, &stat); err != nil {
		return objectinventory.Usage{}, err
	}
	u.DiskBytes, u.AvailableBytes = stat.Blocks*uint64(stat.Bsize), stat.Bavail*uint64(stat.Bsize)
	u.FinishedAt = time.Now().UTC()
	if checkCounters {
		if err := s.auditUsageGlobal(ctx, &u); err != nil {
			if ctx.Err() != nil {
				return objectinventory.Usage{}, ctx.Err()
			}
			s.noteUsageDrift(&u, catalogIndexName)
		}
		s.usageAudit.Store(&usageAuditState{At: u.FinishedAt, Healthy: u.CatalogHealthy && s.requireCatalogIndex() == nil})
	}
	return u, nil
}

func addInventoryIssue(u *objectinventory.Usage, code, path string, failure bool) {
	severity := "warning"
	if failure {
		u.Errors++
		u.CatalogHealthy = false
		severity = "error"
	} else {
		u.Warnings++
	}
	if len(u.Issues) < api.ObjectNodeInventoryMaxIssues {
		u.Issues = append(u.Issues, objectinventory.Issue{Code: code, Path: filepath.ToSlash(path), Severity: severity})
	} else {
		u.IssuesTruncated = true
	}
}

func addInventoryBytes(total *int64, size int64) error {
	if size < 0 || size > api.MaxObjectStoragePolicyValue-*total {
		return ErrCapacity
	}
	*total += size
	return nil
}

func (s *Store) auditBuckets(ctx context.Context, u *objectinventory.Usage, counters bool) error {
	return visitDirectory(filepath.Join(s.root, "buckets"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join("buckets", e.Name())
		var b bucketRecord
		if !e.IsDir() || readJSON(filepath.Join(s.root, dir, "bucket.json"), &b) != nil || nameHash(b.Name) != e.Name() {
			addInventoryIssue(u, "invalid_bucket_catalog", dir, true)
			return nil //nolint:nilerr // Preserve the finding and continue the read-only audit.
		}
		if _, err := s.bucket(b.Name); err != nil {
			addInventoryIssue(u, "invalid_bucket_catalog", dir, true)
			return nil //nolint:nilerr // Preserve the finding and continue the read-only audit.
		}
		u.Buckets++
		beforeObjects, beforeBytes := u.Objects, u.CommittedBytes
		scope := catalogScope(nameHash(b.Name), b.Generation)
		err := visitDirectory(filepath.Join(s.root, dir, "objects"), func(entry os.DirEntry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			path := filepath.Join(dir, "objects", entry.Name())
			if !entry.IsDir() {
				addInventoryIssue(u, "invalid_object_catalog", path, true)
				return nil
			}
			o, err := readObject(filepath.Join(s.root, path))
			if errors.Is(err, ErrNoObject) {
				return nil
			}
			if err != nil {
				addInventoryIssue(u, "invalid_object_catalog", path, true)
				return nil //nolint:nilerr // Preserve the finding and continue the read-only audit.
			}
			u.Objects++
			if err = addInventoryBytes(&u.CommittedBytes, o.Size); err != nil {
				return err
			}
			if inventoryBlob(filepath.Join(s.root, path, o.ID+".data"), o) != nil {
				addInventoryIssue(u, "missing_or_invalid_object_blob", path, true)
			}
			if counters && s.auditUsageObject(scope, entry.Name(), o) != nil {
				s.noteUsageDrift(u, path)
			}
			return nil
		})
		if err == nil && counters {
			if counterErr := s.auditUsageBucket(ctx, scope, u.Objects-beforeObjects, u.CommittedBytes-beforeBytes); counterErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				s.noteUsageDrift(u, dir)
			}
		}
		return err
	})
}

func (s *Store) auditUploads(ctx context.Context, usage *objectinventory.Usage, counters bool) error {
	return visitDirectory(filepath.Join(s.root, "multipart"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join("multipart", e.Name())
		if !e.IsDir() {
			addInventoryIssue(usage, "invalid_upload_catalog", path, true)
			return nil
		}
		u, err := s.readMultipart(e.Name())
		if err != nil {
			addInventoryIssue(usage, "invalid_upload_catalog", path, true)
			return nil //nolint:nilerr // Preserve the finding and continue the read-only audit.
		}
		usage.Sessions++
		if u.Status != multipartActive && u.Status != multipartCompleting {
			if u.Status == multipartCompleted || u.Status == multipartRejected {
				r, err := s.readReceipt(u.ReceiptID)
				expected := ReceiptCommitted
				if u.Status == multipartRejected {
					expected = ReceiptRejected
				}
				if err != nil || r.Kind != "multipart" || r.Bucket != u.Bucket || r.Generation != u.Generation || r.Object.Key != u.Key || r.Status != expected || r.ManifestHash != u.Completion.Hash() {
					addInventoryIssue(usage, "missing_or_invalid_completion_receipt", path, true)
				}
			}
			if counters && s.auditUsageUpload(ctx, u, 0, 0) != nil {
				s.noteUsageDrift(usage, path)
			}
			return nil
		}
		b, err := s.bucket(u.Bucket)
		if err != nil || b.Generation != u.Generation {
			addInventoryIssue(usage, "orphan_upload", path, true)
			return nil //nolint:nilerr // Preserve the finding and continue the read-only audit.
		}
		usage.MultipartUploads++
		beforeParts, beforeBytes := usage.MultipartParts, usage.MultipartBytes
		err = s.auditUploadParts(ctx, usage, u, counters)
		if err == nil && counters {
			if counterErr := s.auditUsageUpload(ctx, u, usage.MultipartParts-beforeParts, usage.MultipartBytes-beforeBytes); counterErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				s.noteUsageDrift(usage, path)
			}
		}
		return err
	})
}

func (s *Store) auditUploadParts(ctx context.Context, usage *objectinventory.Usage, u MultipartUpload, counters bool) error {
	path := filepath.Join("multipart", u.ID, "parts")
	var selected int64
	err := visitDirectory(filepath.Join(s.root, path), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		number, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil || number < 1 || number > api.MaxMultipartParts || strconv.FormatInt(number, 10) != e.Name() || !e.IsDir() {
			addInventoryIssue(usage, "invalid_part_catalog", filepath.Join(path, e.Name()), true)
			return nil //nolint:nilerr // Preserve the finding and continue the read-only audit.
		}
		part, err := s.readPart(u.ID, int32(number))
		if errors.Is(err, ErrInvalidPart) {
			return nil
		}
		if err != nil || part.Object.Key != u.Key {
			addInventoryIssue(usage, "invalid_part_catalog", filepath.Join(path, e.Name()), true)
			return nil //nolint:nilerr // Preserve the finding and continue the read-only audit.
		}
		if part.Object.Size > u.MaxBytes-selected {
			addInventoryIssue(usage, "invalid_upload_size", path, true)
		}
		selected += part.Object.Size
		usage.MultipartParts++
		if err = addInventoryBytes(&usage.MultipartBytes, part.Object.Size); err != nil {
			return err
		}
		if inventoryBlob(filepath.Join(s.root, path, e.Name(), part.Object.ID+".data"), part.Object) != nil {
			addInventoryIssue(usage, "missing_or_invalid_part_blob", filepath.Join(path, e.Name()), true)
		}
		if counters && s.auditUsagePart(u, part) != nil {
			s.noteUsageDrift(usage, filepath.Join(path, e.Name()))
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		addInventoryIssue(usage, "invalid_upload_parts", path, true)
		return nil //nolint:nilerr // Preserve the finding and continue the read-only audit.
	}
	return err
}

// Enumerate fixed-layout directories in streaming batches. Symlinks and special
// files are diagnosed, never followed. Staging may change outside the catalog lock.
func (s *Store) auditFiles(ctx context.Context, u *objectinventory.Usage, relative string, depth int) error {
	if depth > api.ObjectNodeInventoryMaxDepth {
		addInventoryIssue(u, "unexpected_directory_depth", relative, true)
		return nil
	}
	return visitDirectory(filepath.Join(s.root, relative), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(relative, e.Name())
		info, err := e.Info()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			return s.auditFiles(ctx, u, path, depth+1)
		}
		if !info.Mode().IsRegular() {
			addInventoryIssue(u, "unexpected_file_type", path, true)
			return nil
		}
		u.Files++
		if err = addInventoryBytes(&u.FileBytes, info.Size()); err != nil {
			return err
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			if err = addInventoryBytes(&u.AllocatedBytes, st.Blocks*512); err != nil {
				return err
			}
		}
		return s.auditFile(u, path, info.Size())
	})
}

func (s *Store) auditFile(u *objectinventory.Usage, path string, size int64) error {
	if strings.HasPrefix(path, "staging"+string(filepath.Separator)) {
		return addInventoryBytes(&u.StagingBytes, size)
	}
	if strings.HasPrefix(path, "trash"+string(filepath.Separator)) {
		if err := addInventoryBytes(&u.TrashBytes, size); err != nil {
			return err
		}
	}
	if strings.HasSuffix(path, ".data") {
		if err := addInventoryBytes(&u.DataBytes, size); err != nil {
			return err
		}
		if strings.HasPrefix(path, "replication/blobs/") {
			return nil
		}
		if !s.inventoryReferenced(path) {
			u.UnreferencedBlobs++
			addInventoryIssue(u, "unreferenced_blob", path, false)
			return addInventoryBytes(&u.UnreferencedBytes, size)
		}
		return nil
	}
	if strings.HasPrefix(path, "receipts"+string(filepath.Separator)) && strings.HasSuffix(path, ".json") {
		u.Receipts++
		if err := addInventoryBytes(&u.ReceiptBytes, size); err != nil {
			return err
		}
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		if _, err := s.readReceipt(id); err != nil {
			addInventoryIssue(u, "invalid_receipt", path, true)
		}
	}
	if !inventoryMetadata(path) {
		addInventoryIssue(u, "unrecognized_file", path, false)
	}
	return addInventoryBytes(&u.MetadataBytes, size)
}

func inventoryMetadata(path string) bool {
	if validReplicationBackupPath(filepath.ToSlash(path), false) {
		return true
	}
	if strings.HasPrefix(path, "replication/") && strings.HasPrefix(filepath.Base(path), ".tmp-") && validReceiptID(strings.TrimPrefix(filepath.Base(path), ".tmp-")) {
		return true
	}
	p := strings.Split(filepath.ToSlash(path), "/")
	if len(p) >= 2 && p[0] == "scrub" {
		name := p[len(p)-1]
		if strings.HasPrefix(name, ".tmp-") && validReceiptID(strings.TrimPrefix(name, ".tmp-")) {
			return true
		}
		if len(p) == 2 {
			return name == "progress.json" || strings.HasSuffix(name, ".jsonl") && validReceiptID(strings.TrimSuffix(name, ".jsonl"))
		}
		return len(p) == 3 && p[1] == "findings" && strings.HasSuffix(name, ".json") && validScrubFindingID(strings.TrimSuffix(name, ".json"))
	}
	if len(p) == 1 {
		return p[0] == ".lock" || p[0] == "format.json" || p[0] == "recovery.json" || p[0] == recoveryIndexName || p[0] == catalogIndexName
	}
	if len(p) == 2 && p[0] == "receipts" {
		return strings.HasSuffix(p[1], ".json") && validReceiptID(strings.TrimSuffix(p[1], ".json"))
	}
	if len(p) == 3 && p[0] == "buckets" {
		return p[2] == "bucket.json"
	}
	if len(p) == 3 && p[0] == "multipart" {
		return p[2] == "session.json"
	}
	return len(p) == 5 && (p[0] == "buckets" && p[2] == "objects" || p[0] == "multipart" && p[2] == "parts") && p[4] == "current.json"
}

func (s *Store) inventoryReferenced(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) == 5 && parts[0] == "buckets" && parts[2] == "objects" {
		o, err := readObject(filepath.Join(s.root, filepath.Dir(path)))
		return err == nil && parts[4] == o.ID+".data"
	}
	if len(parts) == 5 && parts[0] == "multipart" && parts[2] == "parts" {
		u, err := s.readMultipart(parts[1])
		if err != nil || u.Status != multipartActive && u.Status != multipartCompleting {
			return false
		}
		var p MultipartPart
		if readJSON(filepath.Join(s.root, filepath.Dir(path), "current.json"), &p) != nil {
			return false
		}
		return parts[4] == p.Object.ID+".data"
	}
	return false
}
