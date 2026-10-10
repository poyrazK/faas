package api

// Interactive app-task attach wire contract (ADR-958), shared by the gateway
// endpoint and the CLI. The WebSocket carries binary messages whose first
// byte is the message type.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
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

var ErrAppTaskAttachMessage = errors.New("api: malformed app task attach message")

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
