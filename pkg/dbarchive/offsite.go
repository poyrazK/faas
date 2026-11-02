package dbarchive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The off-site copy (ADR-1027). The bucket keeps every database's archive
// off its node, but it is one bucket at one provider: a suspended account,
// a deleted bucket or a lost region takes every restore point with it. An
// off-site mirror, at another provider, keeps a second copy.
//
// Offsite uploads each local archive file under its own markers,
// independently of the bucket's Uploader, so a slow or unreachable
// off-site provider never delays the bucket's recovery point. Whatever
// the store deletes, the holds it writes and the files a handoff releases
// are first recorded in a local journal, which Offsite replays in order
// on its next pass. Pruning, drops and handoffs never wait on the off-site
// provider, and the mirror deletes only what the store deleted: it never
// infers a deletion from what the bucket lacks, so an emptied bucket
// cannot empty the mirror. Reads fall back to the mirror when the bucket
// fails or lacks an object.

// offsiteJournalDir holds the journal under the store root, beside the
// database directories (ValidDatabaseID rejects its name).
const offsiteJournalDir = ".offsite-journal"

// offsiteOp is one journal entry. Path is relative to the database's
// archive ("wal/<name>", "base/<name>", "holds/<name>"); a drop has none.
type offsiteOp struct {
	Op       string `json:"op"`
	Database string `json:"database"`
	Path     string `json:"path,omitempty"`
}

const (
	// offsiteCopy copies Path from the bucket, if it is still there.
	offsiteCopy = "copy"
	// offsiteDelete deletes Path from the mirror.
	offsiteDelete = "delete"
	// offsiteDrop deletes the database's whole mirror.
	offsiteDrop = "drop"
)

func (op offsiteOp) valid() bool {
	if !ValidDatabaseID(op.Database) {
		return false
	}
	switch op.Op {
	case offsiteDrop:
		return op.Path == ""
	case offsiteCopy, offsiteDelete:
		dir, name, ok := strings.Cut(op.Path, "/")
		return ok && name != "" && !strings.ContainsAny(name, "/\\") && name[0] != '.' && (dir == "wal" || dir == "base" || dir == "holds")
	}
	return false
}

// key is the object key of op's path under prefix.
func (op offsiteOp) key(prefix string) string {
	return strings.TrimSuffix(prefix, "/") + "/" + op.Database + "/" + op.Path
}

func offsiteMarker(dir, databaseID, name string) string {
	return filepath.Join(dir, databaseID, "uploaded-offsite", name)
}

// journalOffsite records op for the mirror; without one it does nothing.
// The entry is durable before the caller acts, so a crash in between
// leaves at worst an entry whose action had not happened yet: a copy of a
// missing object, or a delete of an absent one.
func (s *LocalStore) journalOffsite(op offsiteOp) error {
	if !s.offsite.Load() {
		return nil
	}
	if !op.valid() {
		return fmt.Errorf("dbarchive: invalid off-site journal entry %+v", op)
	}
	dir := filepath.Join(s.dir, offsiteJournalDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(op)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d.json", s.nextOffsiteSeq())
	tmp := filepath.Join(dir, "."+name)
	if err := writeFileSync(tmp, body); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

// nextOffsiteSeq orders journal entries: the clock in nanoseconds, but
// always past the last entry, so a clock stepped back never reorders a
// hold's copy and its removal.
func (s *LocalStore) nextOffsiteSeq() int64 {
	for {
		last := s.offsiteLast.Load()
		next := max(time.Now().UnixNano(), last+1)
		if s.offsiteLast.CompareAndSwap(last, next) {
			return next
		}
	}
}

// BackfillMirror journals a copy of every object of the database's bucket
// archive that the mirror lacks, and returns how many it queued. It is
// for a database whose node is lost: whatever that node had not mirrored
// yet is only in the bucket, and no node uploads it again. It only adds
// copies: what the mirror deletes still comes from the journal alone, and
// a copy of an object deleted meanwhile finds it gone and does nothing.
// Without a mirror it does nothing.
func (s *TieredStore) BackfillMirror(ctx context.Context, databaseID string) (int, error) {
	if !ValidDatabaseID(databaseID) {
		return 0, fmt.Errorf("dbarchive: invalid database id %q", databaseID)
	}
	if s.Remote == nil || s.Offsite == nil || !s.offsite.Load() {
		return 0, nil
	}
	root := strings.TrimSuffix(s.Prefix, "/") + "/" + databaseID + "/"
	keys, err := s.Remote.List(ctx, root)
	if err != nil {
		return 0, err
	}
	mirrorRoot := strings.TrimSuffix(s.OffsitePrefix, "/") + "/" + databaseID + "/"
	mirrored, err := s.Offsite.Sizes(ctx, mirrorRoot)
	if err != nil {
		return 0, err
	}
	slices.Sort(keys)
	queued := 0
	for _, key := range keys {
		rel := strings.TrimPrefix(key, root)
		op := offsiteOp{Op: offsiteCopy, Database: databaseID, Path: rel}
		if _, ok := mirrored[mirrorRoot+rel]; ok || !op.valid() {
			continue
		}
		if err := s.journalOffsite(op); err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}

// Offsite mirrors the store's archive to a second bucket. NewOffsite turns
// the store's journal on; it must run before the store serves.
type Offsite struct {
	Store *LocalStore
	// Bucket is the store's own remote, which journal copies read from.
	Bucket       Remote
	BucketPrefix string
	// Remote is the mirror.
	Remote Remote
	Prefix string
	// OnPending reports the files and journal entries still waiting, and
	// the age of the oldest: the off-site recovery point.
	OnPending func(count int, oldest time.Duration)
	now       func() time.Time
}

// NewOffsite builds the mirror and turns on the store's journal. New
// entries follow those a previous run left.
func NewOffsite(store *LocalStore, bucket Remote, bucketPrefix string, remote Remote, prefix string) *Offsite {
	o := &Offsite{Store: store, Bucket: bucket, BucketPrefix: bucketPrefix, Remote: remote, Prefix: prefix}
	if names, err := o.journal(); err == nil && len(names) > 0 {
		if last, err := strconv.ParseInt(strings.TrimSuffix(names[len(names)-1], ".json"), 10, 64); err == nil {
			store.offsiteLast.Store(last)
		}
	}
	store.offsite.Store(true)
	return o
}

// RunOnce replays the journal, then uploads every local archive file the
// mirror lacks. It returns how many files and entries it applied. A
// failure stops the pass where it is; the next pass resumes there.
func (o *Offsite) RunOnce(ctx context.Context) (int, error) {
	applied, err := o.replay(ctx)
	if err == nil {
		var shipped int
		shipped, err = o.upload(ctx)
		applied += shipped
	}
	if o.OnPending != nil {
		count, oldest := o.pending()
		o.OnPending(count, oldest)
	}
	return applied, err
}

// Run mirrors every interval until ctx ends.
func (o *Offsite) Run(ctx context.Context, interval time.Duration, onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := o.RunOnce(ctx); err != nil && onError != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// journal lists the journal's entries in the order they were written.
func (o *Offsite) journal() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(o.Store.dir, offsiteJournalDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

func (o *Offsite) replay(ctx context.Context) (int, error) {
	names, err := o.journal()
	if err != nil {
		return 0, err
	}
	dir := filepath.Join(o.Store.dir, offsiteJournalDir)
	for i, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path) //nolint:gosec // a journal entry under the store root
		if err != nil {
			return i, err
		}
		var op offsiteOp
		if err := json.Unmarshal(raw, &op); err != nil || !op.valid() {
			// Never written by journalOffsite; applying it could delete the
			// wrong objects.
			return i, fmt.Errorf("dbarchive: corrupt off-site journal entry %s", name)
		}
		if err := o.apply(ctx, op); err != nil {
			return i, fmt.Errorf("dbarchive: off-site %s %s/%s: %w", op.Op, op.Database, op.Path, err)
		}
		if err := os.Remove(path); err != nil {
			return i, err
		}
	}
	return len(names), nil
}

func (o *Offsite) apply(ctx context.Context, op offsiteOp) error {
	switch op.Op {
	case offsiteDelete:
		return o.Remote.Delete(ctx, op.key(o.Prefix))
	case offsiteDrop:
		keys, err := o.Remote.List(ctx, strings.TrimSuffix(o.Prefix, "/")+"/"+op.Database+"/")
		if err != nil {
			return err
		}
		for _, key := range keys {
			if err := o.Remote.Delete(ctx, key); err != nil {
				return err
			}
		}
		return nil
	}
	// A copy stages the object in an unlinked file: Put needs its size.
	tmp, err := os.CreateTemp(o.Store.fetchDir(), "offsite-*")
	if err != nil {
		return err
	}
	_ = os.Remove(tmp.Name())
	defer func() { _ = tmp.Close() }()
	if err := o.Bucket.Get(ctx, op.key(o.BucketPrefix), tmp); errors.Is(err, ErrNotFound) {
		return nil // deleted since; its delete entry follows
	} else if err != nil {
		return err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return o.Remote.Put(ctx, op.key(o.Prefix), tmp, size)
}

// upload ships the local files the mirror lacks. Each file is opened and
// later marked under the store's lock, but uploaded outside it, so the
// mirror never holds up the bucket's uploads, pruning or drops. A file
// pruned or dropped meanwhile is not marked; its journal entry, written
// before it went, deletes the mirror's copy on the next pass.
func (o *Offsite) upload(ctx context.Context) (int, error) {
	shipped := 0
	for _, f := range o.waiting() {
		file, ok := o.open(f)
		if !ok {
			continue
		}
		err := o.put(ctx, file, f)
		_ = file.Close()
		if err != nil {
			return shipped, err
		}
		shipped++
	}
	return shipped, nil
}

// offsiteFile is one local archive file the mirror lacks.
type offsiteFile struct {
	database string
	archiveFile
	modTime time.Time
}

// waiting lists the local archive files without an off-site marker, in
// upload order per database: WAL, then each backup's data before its
// manifest.
func (o *Offsite) waiting() []offsiteFile {
	o.Store.maint.Lock()
	defer o.Store.maint.Unlock()
	databases, err := os.ReadDir(o.Store.dir)
	if err != nil {
		return nil
	}
	lister := Uploader{Store: o.Store, Prefix: o.Prefix}
	var out []offsiteFile
	for _, db := range databases {
		if !db.IsDir() || !ValidDatabaseID(db.Name()) {
			continue
		}
		for _, f := range lister.files(db.Name()) {
			if _, err := os.Stat(offsiteMarker(o.Store.dir, db.Name(), f.name)); err == nil {
				continue
			}
			info, err := os.Stat(f.path)
			if err != nil {
				continue
			}
			out = append(out, offsiteFile{database: db.Name(), archiveFile: f, modTime: info.ModTime()})
		}
	}
	return out
}

func (o *Offsite) open(f offsiteFile) (*os.File, bool) {
	o.Store.maint.Lock()
	defer o.Store.maint.Unlock()
	file, err := os.Open(f.path) //nolint:gosec,forbidigo // a path listed under the store root
	return file, err == nil
}

func (o *Offsite) put(ctx context.Context, file *os.File, f offsiteFile) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := o.Remote.Put(ctx, f.key, file, info.Size()); err != nil {
		return err
	}
	o.Store.maint.Lock()
	defer o.Store.maint.Unlock()
	if _, err := os.Stat(f.path); err != nil {
		return nil //nolint:nilerr // pruned or dropped meanwhile: no marker
	}
	marker := offsiteMarker(o.Store.dir, f.database, f.name)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	return writeFileSync(marker, nil)
}

// pending counts the files and journal entries still waiting and the age
// of the oldest.
func (o *Offsite) pending() (int, time.Duration) {
	now := time.Now
	if o.now != nil {
		now = o.now
	}
	files := o.waiting()
	count := len(files)
	var oldest time.Duration
	for _, f := range files {
		oldest = max(oldest, now().Sub(f.modTime))
	}
	names, _ := o.journal()
	count += len(names)
	if len(names) > 0 {
		if info, err := os.Stat(filepath.Join(o.Store.dir, offsiteJournalDir, names[0])); err == nil {
			oldest = max(oldest, now().Sub(info.ModTime()))
		}
	}
	return count, oldest
}
