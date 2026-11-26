package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

const (
	maxMCPTaskGroupMembers  = 64
	maxMCPTaskGroupManifest = 128 * 1024
	maxMCPTaskGroupReceipt  = 4 * 1024
)

var errInvalidMCPTaskGroup = errors.New("invalid private MCP Task group")

type mcpTaskGroupReference struct {
	SubmissionKey string `json:"submissionKey"`
	TaskID        string `json:"taskId,omitempty"`
}

type mcpTaskGroupPlanMember struct {
	mcpTaskGroupReference
	RequestSHA256 string `json:"requestSha256,omitempty"`
}

type mcpTaskGroupManifest struct {
	Version        int                      `json:"version"`
	ID             string                   `json:"id"`
	EndpointSHA256 string                   `json:"endpointSha256"`
	OwnerBinding   string                   `json:"ownerBinding,omitempty"`
	Members        []mcpTaskGroupPlanMember `json:"members"`
}

type mcpTaskGroupReceipt struct {
	mcpTaskGroupReference
	Version              int    `json:"version"`
	EndpointSHA256       string `json:"endpointSha256"`
	RequestSHA256        string `json:"requestSha256"`
	CredentialProof      string `json:"credentialProof"`
	PlannedSubmissionKey string `json:"plannedSubmissionKey,omitempty"`
}

func loadMCPTaskGroup(path, endpoint string) (*os.Root, mcpTaskGroupManifest, error) {
	var manifest mcpTaskGroupManifest
	path = filepath.Clean(path)
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || !privateMCPTaskGroupFile(info) {
		return nil, manifest, errInvalidMCPTaskGroup
	}
	dir, err := os.OpenRoot(path)
	if err != nil {
		return nil, manifest, errInvalidMCPTaskGroup
	}
	opened, err := dir.Stat(".")
	if err != nil || !os.SameFile(info, opened) || !privateMCPTaskGroupFile(opened) {
		_ = dir.Close()
		return nil, manifest, errInvalidMCPTaskGroup
	}
	manifest, err = readMCPTaskGroupManifest(dir)
	if err != nil {
		_ = dir.Close()
		return nil, manifest, err
	}
	if manifest.EndpointSHA256 != mcpTaskReceiptEndpoint(endpoint) {
		_ = dir.Close()
		return nil, manifest, errors.New("MCP Task group does not match the selected endpoint")
	}
	return dir, manifest, nil
}

func readMCPTaskGroupManifest(dir *os.Root) (mcpTaskGroupManifest, error) {
	var manifest mcpTaskGroupManifest
	body, err := readMCPTaskGroupFile(dir, "group.json", maxMCPTaskGroupManifest)
	if err != nil || decodeCustomerOperationReceipt(body, &manifest) != nil || !manifest.valid() {
		return mcpTaskGroupManifest{}, errInvalidMCPTaskGroup
	}
	return manifest, nil
}

// The Node SDK uses the exact manifest ID as the marker, with no newline.
func recordMCPTaskGroupStop(dir *os.Root, manifest mcpTaskGroupManifest) error {
	current, err := readMCPTaskGroupManifest(dir)
	if err != nil {
		return err
	}
	if current.ID != manifest.ID || current.EndpointSHA256 != manifest.EndpointSHA256 {
		return errors.New("MCP Task group changed")
	}
	const name = "stopped"
	marker, err := readMCPTaskGroupFile(dir, name, 64)
	if errors.Is(err, fs.ErrNotExist) {
		if err := linkPrivateReceiptAt(dir, name, []byte(manifest.ID)); err != nil && !errors.Is(err, fs.ErrExist) {
			return errors.New("could not record MCP Task group stop")
		}
		marker, err = readMCPTaskGroupFile(dir, name, 64)
	}
	if err != nil || string(marker) != manifest.ID {
		return errors.New("invalid MCP Task group stop marker")
	}
	if err := syncPrivateReceiptDirectory(dir); err != nil {
		return errors.New("could not sync MCP Task group stop")
	}
	return nil
}

func (manifest mcpTaskGroupManifest) valid() bool {
	if manifest.Version < 1 || manifest.Version > 3 || !toolpublication.ValidID(manifest.ID) ||
		!toolpublication.ValidID(manifest.EndpointSHA256) || len(manifest.Members) == 0 || len(manifest.Members) > maxMCPTaskGroupMembers {
		return false
	}
	if (manifest.Version == 3 && !toolpublication.ValidID(manifest.OwnerBinding)) ||
		(manifest.Version != 3 && manifest.OwnerBinding != "") {
		return false
	}
	keys, ids := map[string]bool{}, map[string]bool{}
	for _, member := range manifest.Members {
		if !validMCPTaskGroupKey(member.SubmissionKey) || keys[member.SubmissionKey] || !validMCPTaskGroupOptionalID(member.TaskID) {
			return false
		}
		if manifest.Version == 1 {
			if member.RequestSHA256 != "" || (member.TaskID != "" && ids[member.TaskID]) {
				return false
			}
		} else if !toolpublication.ValidID(member.RequestSHA256) || member.TaskID != "" {
			return false
		}
		keys[member.SubmissionKey] = true
		ids[member.TaskID] = true
	}
	return true
}

func validMCPTaskGroupKey(value string) bool {
	key, err := uuid.Parse(value)
	return err == nil && key.Version() == 7 && key.Variant() == uuid.RFC4122 && key.String() == value
}

func validMCPTaskGroupOptionalID(id string) bool {
	return len(id) <= 256 && !strings.ContainsAny(id, "\r\n\x00")
}

func privateMCPTaskGroupFile(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid() && info.Mode().Perm()&0077 == 0
}

func readMCPTaskGroupFile(dir *os.Root, name string, maxBytes int64) ([]byte, error) {
	file, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !privateMCPTaskGroupFile(info) || info.Size() > maxBytes {
		return nil, errInvalidMCPTaskGroup
	}
	body, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes || !utf8.Valid(body) {
		return nil, errInvalidMCPTaskGroup
	}
	return body, nil
}

func (manifest mcpTaskGroupManifest) readMember(dir *os.Root, index int) (mcpTaskGroupReference, error) {
	planned := manifest.Members[index]
	if manifest.Version == 1 {
		return planned.mcpTaskGroupReference, nil
	}
	var receipt mcpTaskGroupReceipt
	body, err := readMCPTaskGroupFile(dir, fmt.Sprintf("member-%d.json", index), maxMCPTaskGroupReceipt)
	if err != nil {
		return mcpTaskGroupReference{}, err
	}
	if decodeCustomerOperationReceipt(body, &receipt) != nil || receipt.EndpointSHA256 != manifest.EndpointSHA256 ||
		receipt.RequestSHA256 != planned.RequestSHA256 || !toolpublication.ValidID(receipt.CredentialProof) ||
		!validMCPTaskGroupKey(receipt.SubmissionKey) || !validMCPTaskGroupOptionalID(receipt.TaskID) {
		return mcpTaskGroupReference{}, errInvalidMCPTaskGroup
	}
	switch receipt.Version {
	case 2:
		if receipt.SubmissionKey == planned.SubmissionKey && receipt.PlannedSubmissionKey == "" {
			return receipt.mcpTaskGroupReference, nil
		}
	case 3:
		if receipt.PlannedSubmissionKey == planned.SubmissionKey && receipt.SubmissionKey != planned.SubmissionKey {
			return receipt.mcpTaskGroupReference, nil
		}
	}
	return mcpTaskGroupReference{}, errInvalidMCPTaskGroup
}
