package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var errInvalidMCPTaskGroupAck = errors.New("invalid MCP Task group acknowledgement marker")

func cmdMCPTaskGroupAck(args []string) int {
	flags := newFlagSet("mcp-task-group-ack", flag.ContinueOnError)
	group := flags.String("group", "", "private group directory saved by the Node SDK")
	updateID := flags.String("update-id", "", "updateId from a Task group watch event")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 || strings.TrimSpace(*group) == "" || !toolpublication.ValidID(*updateID) {
		return printErr("Invalid MCP Task group flags", errors.New("provide --group and a lowercase SHA-256 --update-id"))
	}
	dir, _, err := openMCPTaskGroup(*group)
	if err != nil {
		return printErr("MCP Task group", err)
	}
	defer func() { _ = dir.Close() }()
	if err := acknowledgeMCPTaskGroupUpdate(dir, *updateID); err != nil {
		return printErr("MCP Task group acknowledgement", err)
	}
	return jsonOut(writeJSON(struct {
		UpdateID     string `json:"updateId"`
		Acknowledged bool   `json:"acknowledged"`
	}{UpdateID: *updateID, Acknowledged: true}))
}

// JSON.stringify leaves these characters unescaped. Keep literal escaped
// backslashes opaque so an ID containing "\\u2028" stays distinct from U+2028.
var mcpTaskGroupStringify = strings.NewReplacer(
	`\\`, `\\`, `\u003c`, "<", `\u003e`, ">", `\u0026`, "&", `\u2028`, "\u2028", `\u2029`, "\u2029",
)

func mcpTaskGroupUpdateID(groupID string, index int, task mcphosting.Task) string {
	// This tuple contains only strings and a bounded receipt index.
	body, _ := json.Marshal([]any{groupID, index, task.TaskID, task.CreatedAt, task.LastUpdatedAt, task.Status})
	return toolpublication.Digest([]byte(mcpTaskGroupStringify.Replace(string(body))))
}

func mcpTaskGroupUpdateAcknowledged(dir *os.Root, updateID string) (bool, error) {
	marker, err := openMCPTaskGroupFile(dir, "ack-"+updateID, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, errInvalidMCPTaskGroupAck
	}
	_ = marker.Close()
	return true, nil
}

func acknowledgeMCPTaskGroupUpdate(dir *os.Root, updateID string) error {
	name := "ack-" + updateID
	// The SDK represents an acknowledgement as an empty private file. An
	// exclusive create lets simultaneous acknowledgements converge.
	marker, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if errors.Is(err, fs.ErrExist) {
		marker, err = openMCPTaskGroupFile(dir, name, 0)
	}
	if err != nil {
		return errInvalidMCPTaskGroupAck
	}
	defer func() { _ = marker.Close() }()
	if err := marker.Chmod(0600); err != nil {
		return errors.New("could not secure MCP Task group acknowledgement")
	}
	if err := marker.Sync(); err != nil {
		return errors.New("could not sync MCP Task group acknowledgement")
	}
	if err := syncPrivateReceiptDirectory(dir); err != nil {
		return errors.New("could not sync MCP Task group directory")
	}
	return nil
}
