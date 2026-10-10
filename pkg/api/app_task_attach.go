package api

// Interactive app-task attach wire contract (ADR-958), shared by the gateway
// endpoint and the CLI. The WebSocket carries binary messages whose first
// byte is the message type.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	AppTaskInteractiveDefaultCommand        = "/bin/sh"
	AppTaskInteractiveDefaultTimeoutSeconds = 3600

	// AppTaskAttachSubprotocol is the WebSocket subprotocol of
	// GET /v1/apps/{slug}/tasks/{id}/attach.
	AppTaskAttachSubprotocol = "gregale-app-task-attach-v1"
	// AppTaskAttachTokenHeader carries the one-time attach token.
	AppTaskAttachTokenHeader = "X-Gregale-Attach-Token"

	AppTaskAttachStdin      byte = 0x01
	AppTaskAttachResize     byte = 0x02
	AppTaskAttachStdinClose byte = 0x03

	AppTaskAttachAttached byte = 0x10
	AppTaskAttachStdout   byte = 0x11
	AppTaskAttachStderr   byte = 0x12
	AppTaskAttachTerminal byte = 0x13

	// AppTaskAttachMaxMessageBytes bounds one WebSocket message.
	AppTaskAttachMaxMessageBytes = 64*1024 + 1
)

// Reserved guest-init built-ins for interactive sessions (ADR-958). Both run
// without a tty: copy-out streams a tar of one path read as the app user;
// port-forward relays apptaskmux streams to one host:port dialed from inside
// the app's network.
const (
	AppTaskCopyOutCommand     = "__gregale_copy_out_v1__"
	AppTaskPortForwardCommand = "__gregale_port_forward_v1__"
)

var ErrAppTaskAttachMessage = errors.New("api: malformed app task attach message")

// IsAppTaskSessionBuiltin reports whether command names a reserved
// interactive built-in, valid or not.
func IsAppTaskSessionBuiltin(command []string) bool {
	return len(command) > 0 && (command[0] == AppTaskCopyOutCommand || command[0] == AppTaskPortForwardCommand)
}

// ValidAppTaskSessionBuiltin checks a reserved built-in's exact shape.
func ValidAppTaskSessionBuiltin(command []string, shell, tty bool) bool {
	if shell || tty || len(command) != 2 || command[1] == "" || strings.ContainsRune(command[1], '\x00') {
		return false
	}
	switch command[0] {
	case AppTaskCopyOutCommand:
		return len(command[1]) <= 4096
	case AppTaskPortForwardCommand:
		return ValidPortForwardTarget(command[1])
	default:
		return false
	}
}

// ValidPortForwardTarget accepts host:port with a port in 1..65535.
func ValidPortForwardTarget(target string) bool {
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || len(host) > 253 {
		return false
	}
	value, err := strconv.Atoi(port)
	return err == nil && value >= 1 && value <= 65535
}

// AppTaskAttachPath is the gateway endpoint for one interactive task.
func AppTaskAttachPath(slug, taskID string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/tasks/" + url.PathEscape(taskID) + "/attach"
}

// IsAppTaskAttachPath matches /v1/apps/{slug}/tasks/{id}/attach exactly. The
// gateways route it to the compute-owned attach handler instead of apid.
func IsAppTaskAttachPath(p string) bool {
	const prefix = "/v1/apps/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	parts := strings.Split(p[len(prefix):], "/")
	return len(parts) == 4 && parts[0] != "" && parts[1] == "tasks" && parts[2] != "" && parts[3] == "attach"
}

// AppTaskAttachTerminalMessage is the final server message of a session.
type AppTaskAttachTerminalMessage struct {
	Status   AppTaskStatus   `json:"status"`
	ExitCode *int            `json:"exit_code,omitempty"`
	Failure  *AppTaskFailure `json:"failure,omitempty"`
}

// EncodeAppTaskAttachResize encodes a client resize message.
func EncodeAppTaskAttachResize(rows, cols uint16) []byte {
	msg := make([]byte, 5)
	msg[0] = AppTaskAttachResize
	binary.BigEndian.PutUint16(msg[1:3], rows)
	binary.BigEndian.PutUint16(msg[3:5], cols)
	return msg
}

// DecodeAppTaskAttachResize decodes the payload of a resize message.
func DecodeAppTaskAttachResize(payload []byte) (rows, cols uint16, err error) {
	if len(payload) != 4 {
		return 0, 0, ErrAppTaskAttachMessage
	}
	return binary.BigEndian.Uint16(payload[:2]), binary.BigEndian.Uint16(payload[2:]), nil
}

// EncodeAppTaskAttachTerminal encodes the final server message.
func EncodeAppTaskAttachTerminal(terminal AppTaskAttachTerminalMessage) ([]byte, error) {
	body, err := json.Marshal(terminal)
	if err != nil {
		return nil, err
	}
	return append([]byte{AppTaskAttachTerminal}, body...), nil
}

// DecodeAppTaskAttachTerminal decodes the payload of a terminal message.
func DecodeAppTaskAttachTerminal(payload []byte) (AppTaskAttachTerminalMessage, error) {
	var terminal AppTaskAttachTerminalMessage
	if err := json.Unmarshal(payload, &terminal); err != nil || terminal.Status == "" {
		return AppTaskAttachTerminalMessage{}, ErrAppTaskAttachMessage
	}
	return terminal, nil
}
