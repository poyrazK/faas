package storage

import "testing"

func TestAppendRangeMergesTouchingRuns(t *testing.T) {
	var rs []fileRange
	rs = appendRange(rs, 0, 4096)
	rs = appendRange(rs, 4096, 8192) // touches: merged
	rs = appendRange(rs, 16384, 4096)
	if len(rs) != 2 || rs[0] != (fileRange{0, 12288}) || rs[1] != (fileRange{16384, 4096}) {
		t.Fatalf("ranges = %+v", rs)
	}
}
