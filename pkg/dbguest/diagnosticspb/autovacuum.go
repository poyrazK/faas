package diagnosticspb

import (
	"math"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func EncodeAutovacuum(s *dbguest.AutovacuumSettings) *vmmdpb.DatabaseAutovacuumSettings {
	if s == nil {
		return nil
	}
	return &vmmdpb.DatabaseAutovacuumSettings{VacuumScaleFactor: s.VacuumScaleFactor, AnalyzeScaleFactor: s.AnalyzeScaleFactor}
}

func DecodeAutovacuum(s *vmmdpb.DatabaseAutovacuumSettings) *dbguest.AutovacuumSettings {
	if s == nil || !validScaleFactor(s.GetVacuumScaleFactor()) || !validScaleFactor(s.GetAnalyzeScaleFactor()) {
		return nil
	}
	return &dbguest.AutovacuumSettings{VacuumScaleFactor: s.GetVacuumScaleFactor(), AnalyzeScaleFactor: s.GetAnalyzeScaleFactor()}
}

func validScaleFactor(n float64) bool {
	return n >= 0 && n <= 100 && !math.IsNaN(n)
}
