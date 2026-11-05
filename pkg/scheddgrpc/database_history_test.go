package scheddgrpc

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dbsched"
)

func TestHistoricalMajorMetadataIsExplicitAndComplete(t *testing.T) {
	until := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, history := range [][]dbsched.MajorSpan{nil, {{Major: 16, Until: until}, {Major: 17, Until: until.Add(time.Hour)}}} {
		vm := dbsched.VM{DatabaseID: testDBID, Major: 18, MajorHistory: history}
		rt := databaseRuntime(vm)
		if !rt.GetMajorHistoryComplete() || len(rt.GetMajorHistory()) != len(history) {
			t.Fatal("complete history was lost", rt)
		}
		for i, span := range history {
			if rt.GetMajorHistory()[i].GetPostgresMajor() != int32(span.Major) || !rt.GetMajorHistory()[i].GetUntil().AsTime().Equal(span.Until) {
				t.Fatal("history boundary changed", rt)
			}
		}
	}
}
