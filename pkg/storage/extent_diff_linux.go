//go:build linux

package storage

// mappedExtents lists path's allocated extents at their file offsets. The
// walk syncs delayed allocations first; an extent still without a placement
// reports physical 0 and is marked unknown.
func mappedExtents(path string) ([]mappedExtent, error) {
	var out []mappedExtent
	err := walkExtents(path, func(logical, physical, length int64, _ bool) {
		if physical == 0 {
			physical = -1
		}
		out = append(out, mappedExtent{logical: logical, physical: physical, length: length})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
