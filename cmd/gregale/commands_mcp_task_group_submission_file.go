package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

const mcpTaskGroupSubmissionWindow = time.Minute

func openMCPTaskGroupSubmission(ctx context.Context, client *mcphosting.Client, path string, plan []mcpTaskGroupSubmission) (*os.Root, mcpTaskGroupManifest, error) {
	path, err := filepath.Abs(path)
	if err != nil || filepath.Dir(path) == path {
		return nil, mcpTaskGroupManifest{}, errInvalidMCPTaskGroup
	}
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, mcpTaskGroupManifest{}, errors.New("Task group requires an existing parent directory")
	}
	defer func() { _ = parent.Close() }()
	name := filepath.Base(path)
	// Match the SDK's private directory publication rule.
	info, err := parent.Stat(".")
	if err != nil || (info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0) {
		return nil, mcpTaskGroupManifest{}, errors.New("Task group requires a private or sticky parent directory")
	}
	if err := parent.Mkdir(name, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, mcpTaskGroupManifest{}, errors.New("could not create private Task group directory")
	}
	dir, err := openMCPTaskGroupDirectory(parent, name)
	if err != nil {
		return nil, mcpTaskGroupManifest{}, errInvalidMCPTaskGroup
	}
	manifest, err := prepareMCPTaskGroupManifest(ctx, client, dir, plan)
	if err != nil {
		_ = dir.Close()
		return nil, mcpTaskGroupManifest{}, err
	}
	if err := syncMCPTaskGroupPlan(parent, dir); err != nil {
		_ = dir.Close()
		return nil, mcpTaskGroupManifest{}, err
	}
	return dir, manifest, nil
}

func syncMCPTaskGroupPlan(parent, dir *os.Root) error {
	for _, root := range []*os.Root{dir, parent} {
		if err := syncPrivateReceiptDirectory(root); err != nil {
			return errors.New("could not sync Task group plan")
		}
	}
	return nil
}

func prepareMCPTaskGroupManifest(ctx context.Context, client *mcphosting.Client, dir *os.Root, plan []mcpTaskGroupSubmission) (mcpTaskGroupManifest, error) {
	manifest, err := readMCPTaskGroupManifest(dir)
	if errors.Is(err, fs.ErrNotExist) {
		manifest, err = publishMCPTaskGroupManifest(ctx, client, dir, plan)
	}
	if err != nil {
		return manifest, err
	}
	if manifest.Version != 3 || manifest.EndpointSHA256 != mcpTaskReceiptEndpoint(client.Endpoint) || len(manifest.Members) != len(plan) {
		return manifest, errors.New("saved owner-bound Task group does not match the selected endpoint or plan")
	}
	for i, item := range plan {
		if manifest.Members[i].RequestSHA256 != item.requestSHA256 {
			return manifest, fmt.Errorf("Task group plan member %d does not match the saved request", i)
		}
	}
	return manifest, nil
}

func publishMCPTaskGroupManifest(ctx context.Context, client *mcphosting.Client, dir *os.Root, plan []mcpTaskGroupSubmission) (mcpTaskGroupManifest, error) {
	files, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return mcpTaskGroupManifest{}, errInvalidMCPTaskGroup
	}
	for _, file := range files {
		// A crashed publication can leave its private temporary file. A
		// journal with existing records must keep its original manifest.
		if !strings.HasPrefix(file.Name(), customerOperationReceiptTempPrefix) {
			current, err := readMCPTaskGroupManifest(dir)
			if errors.Is(err, fs.ErrNotExist) {
				return current, errors.New("refusing to replace a Task group with records and a missing manifest")
			}
			return current, err
		}
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return mcpTaskGroupManifest{}, err
	}
	owner, err := client.BindTaskGroup(ctx, hex.EncodeToString(nonce[:]))
	if err != nil {
		return mcpTaskGroupManifest{}, errors.New("MCP server did not authorize recoverable owner-bound Task submission")
	}
	manifest := mcpTaskGroupManifest{
		Version: 3, ID: owner.GroupID, OwnerBinding: owner.Binding,
		EndpointSHA256: mcpTaskReceiptEndpoint(client.Endpoint), Members: make([]mcpTaskGroupPlanMember, len(plan)),
	}
	for i, item := range plan {
		key, err := uuid.NewV7()
		if err != nil {
			return mcpTaskGroupManifest{}, err
		}
		manifest.Members[i] = mcpTaskGroupPlanMember{
			mcpTaskGroupReference: mcpTaskGroupReference{SubmissionKey: key.String()}, RequestSHA256: item.requestSHA256,
		}
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return mcpTaskGroupManifest{}, err
	}
	if err := linkPrivateReceiptAt(dir, "group.json", body); err != nil && !errors.Is(err, fs.ErrExist) {
		return mcpTaskGroupManifest{}, errors.New("could not record Task group plan")
	}
	return readMCPTaskGroupManifest(dir)
}

func checkMCPTaskGroupOpen(dir *os.Root, manifest mcpTaskGroupManifest) error {
	current, err := readMCPTaskGroupManifest(dir)
	if err != nil || current.ID != manifest.ID || current.EndpointSHA256 != manifest.EndpointSHA256 ||
		current.OwnerBinding != manifest.OwnerBinding || !slices.Equal(current.Members, manifest.Members) {
		return errors.New("Task group plan changed")
	}
	if _, err := dir.Lstat("stopped"); !errors.Is(err, fs.ErrNotExist) {
		return errors.New("Task group has a stop marker or its state cannot be verified")
	}
	return nil
}

func newMCPTaskGroupAttempt(manifest mcpTaskGroupManifest, index int, token string) (mcpTaskGroupReceipt, error) {
	member := manifest.Members[index]
	key, err := uuid.Parse(member.SubmissionKey)
	if err != nil {
		return mcpTaskGroupReceipt{}, errInvalidMCPTaskGroup
	}
	receipt := mcpTaskGroupReceipt{
		mcpTaskGroupReference: member.mcpTaskGroupReference, Version: 2,
		EndpointSHA256: manifest.EndpointSHA256, RequestSHA256: member.RequestSHA256,
	}
	if mcpTaskGroupKeyAge(key) >= mcpTaskGroupSubmissionWindow {
		key, err = uuid.NewV7()
		if err != nil {
			return mcpTaskGroupReceipt{}, err
		}
		receipt.Version, receipt.PlannedSubmissionKey = 3, member.SubmissionKey
		receipt.SubmissionKey = key.String()
	}
	if mcpTaskGroupKeyAge(key) < 0 {
		return mcpTaskGroupReceipt{}, errors.New("Task group submission key is ahead of the local clock")
	}
	proof := hmac.New(sha256.New, []byte(token))
	_, _ = fmt.Fprintf(proof, "gregale/node-sdk/mcp-task-receipt/v%d:%s%s%s%s",
		receipt.Version, receipt.EndpointSHA256, receipt.SubmissionKey, receipt.RequestSHA256, receipt.PlannedSubmissionKey)
	receipt.CredentialProof = hex.EncodeToString(proof.Sum(nil))
	return receipt, nil
}

func mcpTaskGroupKeyAge(key uuid.UUID) time.Duration {
	seconds, nanos := key.Time().UnixTime()
	return time.Since(time.Unix(seconds, nanos))
}

func recordMCPTaskGroupAttempt(dir *os.Root, index int, receipt mcpTaskGroupReceipt) error {
	body, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := linkPrivateReceiptAt(dir, mcpTaskGroupReceiptName(index), body); err != nil {
		return err
	}
	return syncPrivateReceiptDirectory(dir)
}
