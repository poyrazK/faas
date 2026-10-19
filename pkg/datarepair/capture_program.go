package datarepair

import (
	_ "embed"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

//go:embed capture_catalog.sql
var captureCatalogSQL string

//go:embed capture_enrollment.sql
var captureEnrollmentSQL string

type captureProgramMessage struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Action  string `json:"action"`
}
type captureProbeProgram struct {
	Version             int                              `json:"version"`
	TimeoutMS           int                              `json:"timeout_ms"`
	MaxResponseBytes    int                              `json:"max_response_bytes"`
	MaxObjects          int                              `json:"max_objects"`
	MaxTableBytes       int                              `json:"max_table_bytes"`
	MaxContextBytes     int                              `json:"max_context_bytes"`
	FindingIDs          []string                         `json:"finding_ids"`
	Messages            map[string]captureProgramMessage `json:"messages"`
	SetupSQL            string                           `json:"setup_sql"`
	CatalogSQL          string                           `json:"catalog_sql"`
	EnrollmentSQL       string                           `json:"enrollment_sql"`
	PythonCatalogSQL    string                           `json:"python_catalog_sql"`
	PythonEnrollmentSQL string                           `json:"python_enrollment_sql"`
}

func captureProgram() captureProbeProgram {
	functions, fingerprint := doctorFunctions()
	functions = slices.DeleteFunc(functions, func(f doctorFunction) bool {
		return !slices.Contains([]string{"begin_operation", "finish_operation", "check_enrollment", "fingerprint", "guard_row", "capture_row", "protect_operation", "protect_change", "require_finalized"}, f.Name)
	})
	for i := range functions {
		functions[i].Runtime = slices.Contains([]string{"begin_operation", "finish_operation", "check_enrollment", "fingerprint"}, functions[i].Name)
	}
	columns := slices.DeleteFunc(doctorColumns(), func(c doctorColumn) bool { return c.Table == "plans" || c.Table == "retention_runs" })
	quote := func(v any) string { return "'" + strings.ReplaceAll(doctorJSON(v), "'", "''") + "'::jsonb" }
	catalog := strings.NewReplacer(
		"__FUNCTIONS_SQL__", strings.ReplaceAll(doctorFunctionSQL, "$1::jsonb", quote(functions)),
		"__INSTALLATION_SQL__", strings.ReplaceAll(doctorInstallationSQL, "$1::jsonb", quote(columns)),
		"__INTERNAL_TRIGGERS_SQL__", doctorInternalTriggersSQL,
		"__PERMISSIONS_SQL__", doctorPermissionsSQL,
	).Replace(captureCatalogSQL)
	enrollment := strings.NewReplacer("__MAX_OBJECTS__", strconv.Itoa(api.DataRepairDoctorMaxObjects), "__FINGERPRINT_SQL__", strings.ReplaceAll(fingerprint, "$1::oid", "e.relation::oid")).Replace(captureEnrollmentSQL)
	pythonSQL := func(sql string) string { return strings.ReplaceAll(strings.ReplaceAll(sql, "%", "%%"), "$1", "%s") }
	messages := map[string]captureProgramMessage{}
	for code, m := range captureMessages() {
		if m.id == "" || slices.Contains(captureFindingIDs(), m.id) {
			messages[code] = captureProgramMessage{m.id, m.status, m.summary, m.action}
		}
	}
	return captureProbeProgram{Version: 1, TimeoutMS: api.DataRepairDoctorTimeoutSeconds * 1000, MaxResponseBytes: api.DataRepairDoctorMaxResponseBytes, MaxObjects: api.DataRepairDoctorMaxObjects, MaxTableBytes: api.DataRepairSourceMaxFilterBytes, MaxContextBytes: api.DataRepairCaptureContextMaxBytes,
		FindingIDs: captureFindingIDs(), Messages: messages, SetupSQL: "SELECT set_config('search_path','pg_catalog',true),set_config('statement_timeout','" + strconv.Itoa(api.DataRepairDoctorTimeoutSeconds*1000) + "',true),set_config('lock_timeout','" + strconv.Itoa(api.DataRepairDoctorTimeoutSeconds*1000) + "',true)", CatalogSQL: catalog, EnrollmentSQL: enrollment, PythonCatalogSQL: pythonSQL(catalog), PythonEnrollmentSQL: pythonSQL(enrollment)}
}

// CaptureReadinessProgram is the trusted, generated client probe shared by the
// three leaf SDKs. It is never accepted from a request or installed in a database.
func CaptureReadinessProgram() []byte {
	raw, _ := json.MarshalIndent(captureProgram(), "", "  ")
	return append(raw, '\n')
}
