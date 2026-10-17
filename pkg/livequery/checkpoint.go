package livequery

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Checkpoints (ADR-1384). A checkpoint is the engine's state as of its last
// round: every registration, and each member's caches and windows, all
// reflecting that round's snapshot S. A restored engine streams from the
// slot's confirmed position; transactions the slot delivers again that are
// visible in S were applied already and are skipped. A checkpointing engine
// confirms the slot only when a checkpoint is written, so the slot never
// moves past state that a crash could lose.
//
// Plans are not stored: a restored engine compiles each query again. When
// the plan differs from the one the state was built with, or the slot was
// lost, the members start over and subscribers receive a full result.

const checkpointVersion = 1

type checkpointFile struct {
	Version int
	Round   uint64
	Applied string // the last round's snapshot
	Groups  []checkpointGroup
}

type checkpointGroup struct {
	SQL         string
	Fingerprint string
	Members     []checkpointMember
}

type checkpointMember struct {
	IDs     []string
	Args    string // encodeValues of the arguments as PostgreSQL text
	Ready   bool
	Columns []string
	Result  *checkpointSet
	Input   *checkpointSet
	Join    *checkpointSet
	Semi    []checkpointSet
	Groups  []checkpointRow // ID is the group key encoding
	Agg     []checkpointAgg
	Window  []string
}

type checkpointSet struct {
	Relations int
	Rows      []checkpointRow
}

type checkpointRow struct {
	ID      string
	Values  string
	Aggs    string
	Sort    string
	Lineage []string
	Group   string
	Enc     string
}

type checkpointAgg struct {
	Enc  string
	Key  string
	N    int64
	Vals []checkpointAggValue
	Rows []string
}

type checkpointAggValue struct {
	Nonnull     int64
	Sum         string
	Scale       int
	Scales      map[int]int64
	Special     int64
	Ext         string
	HasExt      bool
	Stale       bool
	Cands       string
	Override    string
	HasOverride bool
}

// Fingerprint identifies a compiled plan: equal fingerprints mean equal
// maintenance statements, shapes and types.
func (p *Plan) Fingerprint() string {
	data, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// WriteCheckpoint writes the engine's state as of its last round and then
// lets the slot release what that state covers. The writer must hold the
// checkpoint durably when it returns (WriteCheckpointFile does). It must not
// run concurrently with Round.
func (e *Engine) WriteCheckpoint(w io.Writer) error {
	if err := e.encodeCheckpoint(w); err != nil {
		return err
	}
	e.confirmCheckpoint()
	return nil
}

func (e *Engine) confirmCheckpoint() {
	if e.safe > 0 {
		e.capture.Confirm(e.safe)
	}
}

func (e *Engine) encodeCheckpoint(w io.Writer) error {
	f := checkpointFile{Version: checkpointVersion, Round: e.round}
	if e.applied != nil {
		f.Applied = e.applied.String()
	}
	for _, g := range e.groups {
		cg := checkpointGroup{SQL: g.plan.SQL, Fingerprint: g.plan.Fingerprint()}
		for _, m := range g.members {
			cg.Members = append(cg.Members, m.checkpoint())
		}
		f.Groups = append(f.Groups, cg)
	}
	if err := gob.NewEncoder(w).Encode(&f); err != nil {
		return fmt.Errorf("livequery: write checkpoint: %w", err)
	}
	return nil
}

// WriteCheckpointFile writes a checkpoint atomically: to a temporary file
// that is synced and renamed over path.
func (e *Engine) WriteCheckpointFile(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("livequery: create checkpoint: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	err = e.encodeCheckpoint(tmp)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("livequery: write checkpoint %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("livequery: install checkpoint %s: %w", path, err)
	}
	// The directory is opened only to make the rename durable.
	if dir, err := os.Open(filepath.Dir(path)); err == nil { //nolint:forbidigo // livequeryd's state directory, not a customer path
		_ = dir.Sync()
		_ = dir.Close()
	}
	e.confirmCheckpoint()
	return nil
}

func (m *member) checkpoint() checkpointMember {
	cm := checkpointMember{IDs: m.ids, Args: encodeValues(m.argText), Ready: m.ready, Columns: m.columns}
	if !m.ready {
		return cm
	}
	cm.Result, cm.Input, cm.Join = saveSet(m.result), saveSet(m.input), saveSet(m.join)
	for _, s := range m.semi {
		cm.Semi = append(cm.Semi, *saveSet(s))
	}
	for enc, r := range m.groups {
		cm.Groups = append(cm.Groups, saveRow(enc, r))
	}
	for enc, ag := range m.agg {
		ca := checkpointAgg{Enc: enc, Key: encodeValues(ag.key), N: ag.n}
		for id := range ag.rows {
			ca.Rows = append(ca.Rows, id)
		}
		for _, v := range ag.vals {
			cv := checkpointAggValue{Nonnull: v.nonnull, Scale: v.scale, Scales: v.scales, Special: v.special,
				Stale: v.stale, Cands: encodeValues(v.cands)}
			if v.sum != nil {
				cv.Sum = v.sum.String()
			}
			if v.ext != nil {
				cv.Ext, cv.HasExt = *v.ext, true
			}
			if v.override != nil {
				cv.Override, cv.HasOverride = *v.override, true
			}
			ca.Vals = append(ca.Vals, cv)
		}
		cm.Agg = append(cm.Agg, ca)
	}
	if m.win != nil {
		cm.Window = m.win.ids
	}
	return cm
}

func saveSet(s *keyedSet) *checkpointSet {
	if s == nil {
		return nil
	}
	cs := &checkpointSet{Relations: len(s.idx)}
	for id, r := range s.rows {
		cs.Rows = append(cs.Rows, saveRow(id, r))
	}
	return cs
}

func saveRow(id string, r *keyedRow) checkpointRow {
	return checkpointRow{ID: id, Values: encodeValues(r.values), Aggs: encodeValues(r.aggs), Sort: encodeValues(r.sort),
		Lineage: r.lineage, Group: r.group, Enc: r.enc}
}

// RestoreEngine builds an engine from a checkpoint over an open slot. The
// engine confirms the slot only through later checkpoints. Subscriptions
// whose query no longer compiles are closed with schema_changed at the first
// round.
func RestoreEngine(ctx context.Context, pool *pgxpool.Pool, capture *Capture, r io.Reader) (*Engine, error) {
	return RestoreEngineWith(ctx, pool, capture, r, nil)
}

// RestoreEngineWith restores with a compiler (nil compiles in process).
func RestoreEngineWith(ctx context.Context, pool *pgxpool.Pool, capture *Capture, r io.Reader, compiler Compiler) (*Engine, error) {
	var f checkpointFile
	if err := gob.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("livequery: read checkpoint: %w", err)
	}
	if f.Version != checkpointVersion {
		return nil, fmt.Errorf("livequery: checkpoint version %d, want %d", f.Version, checkpointVersion)
	}
	e := NewEngine(pool, capture)
	e.Checkpointed = true
	e.Compiler = compiler
	e.round = f.Round
	if f.Applied != "" && !capture.Created {
		snap, err := ParseSnapshot(f.Applied)
		if err != nil {
			return nil, err
		}
		e.applied = &snap
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("livequery: acquire for restore: %w", err)
	}
	defer conn.Release()
	for _, cg := range f.Groups {
		plan, err := e.compile(ctx, conn.Conn(), cg.SQL)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, ErrCompilerUnavailable) {
				return nil, err
			}
			qe := &QueryError{Code: SchemaChanged, Message: err.Error()}
			for _, cm := range cg.Members {
				for _, id := range cm.IDs {
					e.closing = append(e.closing, Delta{Query: id, Closed: qe})
				}
			}
			continue
		}
		g := &planGroup{plan: plan, byArgs: map[string]*member{}, stats: &e.Stats, engine: e}
		e.bySQL[cg.SQL] = g
		e.groups = append(e.groups, g)
		keep := !capture.Created && plan.Fingerprint() == cg.Fingerprint
		for _, cm := range cg.Members {
			if err := e.restoreMember(g, cm, keep); err != nil {
				return nil, err
			}
		}
	}
	return e, nil
}

func (e *Engine) restoreMember(g *planGroup, cm checkpointMember, keep bool) error {
	text, err := decodeValues(cm.Args)
	if err != nil {
		return err
	}
	args := make([]any, len(text))
	for i, v := range text {
		args[i] = v
	}
	m := &member{group: g, idx: int32(len(g.members)), ids: cm.IDs, args: args, argText: text}
	if err := m.reset(); err != nil {
		return err
	}
	g.members = append(g.members, m)
	g.byArgs[encodeValues(text)] = m
	for _, id := range cm.IDs {
		e.byID[id] = m
	}
	if !keep || !cm.Ready {
		return nil
	}
	if m.result, err = loadSet(cm.Result); err != nil {
		return err
	}
	if m.input, err = loadSet(cm.Input); err != nil {
		return err
	}
	if m.join, err = loadSet(cm.Join); err != nil {
		return err
	}
	m.semi = make([]*keyedSet, len(cm.Semi))
	for i := range cm.Semi {
		if m.semi[i], err = loadSet(&cm.Semi[i]); err != nil {
			return err
		}
	}
	if g.plan.Mode == ModeGroup {
		m.groups = map[string]*keyedRow{}
		for _, cr := range cm.Groups {
			r, err := loadRow(cr)
			if err != nil {
				return err
			}
			m.groups[cr.ID] = r
		}
	}
	if g.plan.Agg != nil {
		if m.agg, err = loadAgg(cm.Agg); err != nil {
			return err
		}
	}
	if m.win != nil {
		m.win.ids = cm.Window
	}
	m.columns = cm.Columns
	m.ready = true
	return nil
}

func loadSet(cs *checkpointSet) (*keyedSet, error) {
	if cs == nil {
		return nil, nil
	}
	s := newKeyedSet(cs.Relations)
	for _, cr := range cs.Rows {
		r, err := loadRow(cr)
		if err != nil {
			return nil, err
		}
		s.put(cr.ID, r)
	}
	return s, nil
}

func loadRow(cr checkpointRow) (*keyedRow, error) {
	r := &keyedRow{lineage: cr.Lineage, group: cr.Group, enc: cr.Enc}
	var err error
	if r.values, err = decodeValues(cr.Values); err != nil {
		return nil, err
	}
	if r.aggs, err = decodeValues(cr.Aggs); err != nil {
		return nil, err
	}
	if r.sort, err = decodeValues(cr.Sort); err != nil {
		return nil, err
	}
	return r, nil
}

func loadAgg(cas []checkpointAgg) (map[string]*aggGroup, error) {
	out := make(map[string]*aggGroup, len(cas))
	for _, ca := range cas {
		key, err := decodeValues(ca.Key)
		if err != nil {
			return nil, err
		}
		ag := newAggGroup(key, len(ca.Vals))
		ag.n = ca.N
		for _, id := range ca.Rows {
			ag.rows[id] = struct{}{}
		}
		for j, cv := range ca.Vals {
			v := &ag.vals[j]
			v.nonnull, v.scale, v.scales, v.special, v.stale = cv.Nonnull, cv.Scale, cv.Scales, cv.Special, cv.Stale
			if cv.Sum != "" {
				sum, ok := new(big.Int).SetString(cv.Sum, 10)
				if !ok {
					return nil, fmt.Errorf("livequery: checkpoint sum %q", cv.Sum)
				}
				v.sum = sum
			}
			if cv.HasExt {
				ext := cv.Ext
				v.ext = &ext
			}
			if cv.HasOverride {
				o := cv.Override
				v.override = &o
			}
			if v.cands, err = decodeValues(cv.Cands); err != nil {
				return nil, err
			}
		}
		out[ca.Enc] = ag
	}
	return out, nil
}
