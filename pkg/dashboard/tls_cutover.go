package dashboard

import (
	"bufio"
	"errors"
	"os"
	"strings"
)

const DefaultTLSCutoverStateFile = "/var/lib/faas/tls-cutover.state"

// TLSCutoverState is the redacted operator state written by the TLS drill.
// It deliberately contains no provider credentials, cookies, or certificate
// material; the admin dashboard only needs the lifecycle marker and audit
// metadata to keep the banner visible after rollback.
type TLSCutoverState struct {
	State     string
	RunID     string
	UpdatedAt string
	Operator  string
	Message   string
}

// ReadTLSCutoverState reads the line-oriented state file written by
// faas-tls-cutover-drill.sh. Missing state is a normal pre-drill condition and
// is returned as an empty value with os.ErrNotExist for callers that want to
// render an explicit "no drill recorded" state.
func ReadTLSCutoverState(path string) (TLSCutoverState, error) {
	var state TLSCutoverState
	f, err := os.Open(path) //nolint:forbidigo // path is the operator-controlled TLS drill state file, never a customer path.
	if err != nil {
		return state, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "state":
			state.State = value
		case "run_id":
			state.RunID = value
		case "updated_at":
			state.UpdatedAt = value
		case "operator":
			state.Operator = value
		case "message":
			state.Message = value
		}
	}
	if err := scanner.Err(); err != nil {
		return TLSCutoverState{}, err
	}
	return state, nil
}

func IsMissingTLSCutoverState(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
