package executor

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestExecutionSetupDiagnosticsOmitPrivateErrorText(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	logExecutionSetupFailure(api.ExecutionRuntimeNode24, "start_interpreter", &os.PathError{
		Op: "exec private-source", Path: "/tmp/private-source-secret", Err: os.ErrPermission,
	})
	log := output.String()
	if strings.Contains(log, "private-source") || !strings.Contains(log, `"error_class":"permission_denied"`) || !strings.Contains(log, `"stage":"start_interpreter"`) {
		t.Fatalf("setup diagnostic violated its privacy contract: %s", log)
	}
}
