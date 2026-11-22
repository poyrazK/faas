package storage

// adr: 20261122132055 §4 — a session volume's backup uploads only the blocks
// written since the last one. The node keeps a reflink clone of the volume as
// of that backup; a new clone shares every block the guest has not rewritten
// since, so where both clones map an offset to the same physical block the
// bytes are the same.

import (
	"errors"
	"sort"
)

// ErrExtentsUnsupported reports a platform or filesystem that cannot map a
// file's extents; the caller then treats the whole file as changed.
var ErrExtentsUnsupported = errExtentsUnsupported

// Range is a half-open byte range [Start, End) of a file.
type Range struct{ Start, End int64 }

// mappedExtent is an extent at a file offset; physical < 0 means its
// placement is unknown, so it never matches another extent.
type mappedExtent struct{ logical, physical, length int64 }

// ChangedRanges lists the byte ranges whose contents may differ between two
// reflink clones of one file, prev (as of the last backup) and cur. A range
// either file maps to another physical block, or maps in one file and not the
// other, is changed; the ranges come sorted and merged.
func ChangedRanges(prev, cur string) ([]Range, error) {
	p, err := mappedExtents(prev)
	if err != nil {
		return nil, err
	}
	c, err := mappedExtents(cur)
	if err != nil {
		return nil, err
	}
	return diffExtents(p, c), nil
}

// DataRanges lists the byte ranges of path that hold data; the rest are
// holes that read as zeros.
func DataRanges(path string) ([]Range, error) {
	c, err := mappedExtents(path)
	if err != nil {
		return nil, err
	}
	return diffExtents(nil, c), nil
}

// diffExtents compares two extent maps. Each map's extents may be split
// differently, so the comparison walks every boundary of both.
func diffExtents(prev, cur []mappedExtent) []Range {
	sortExtents(prev)
	sortExtents(cur)
	cuts := make([]int64, 0, 2*(len(prev)+len(cur)))
	for _, set := range [][]mappedExtent{prev, cur} {
		for _, e := range set {
			cuts = append(cuts, e.logical, e.logical+e.length)
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	var out []Range
	pi, ci := 0, 0
	for i := 0; i+1 < len(cuts); i++ {
		a, b := cuts[i], cuts[i+1]
		if a == b {
			continue
		}
		pp, pok := physicalAt(prev, &pi, a)
		cp, cok := physicalAt(cur, &ci, a)
		if !pok && !cok {
			continue // a hole in both
		}
		if pok && cok && pp >= 0 && pp == cp {
			continue
		}
		if n := len(out); n > 0 && out[n-1].End == a {
			out[n-1].End = b
		} else {
			out = append(out, Range{Start: a, End: b})
		}
	}
	return out
}

func sortExtents(es []mappedExtent) {
	sort.Slice(es, func(i, j int) bool { return es[i].logical < es[j].logical })
}

// physicalAt is the physical address of offset off in es, advancing *i past
// extents that end at or before it; false when off is in a hole.
func physicalAt(es []mappedExtent, i *int, off int64) (int64, bool) {
	for *i < len(es) && es[*i].logical+es[*i].length <= off {
		*i++
	}
	if *i >= len(es) || es[*i].logical > off {
		return 0, false
	}
	e := es[*i]
	if e.physical < 0 {
		return -1, true
	}
	return e.physical + (off - e.logical), true
}

// CloneFile makes dst a point-in-time copy of src: a reflink clone where the
// filesystem shares blocks, and otherwise a copy of src's data regions that
// leaves its holes sparse. cloned reports the reflink.
func CloneFile(dst, src string) (cloned bool, err error) {
	if dst == "" || src == "" {
		return false, errors.New("storage: clone needs both paths")
	}
	return cloneFile(dst, src)
}
