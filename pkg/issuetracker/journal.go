package issuetracker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrOutcomeUnknown = errors.New("ticket creation outcome is unknown; recover the existing ticket with --recover-url before retrying")
var ErrBusy = errors.New("another process is handling this ticket request")
var ErrConflict = errors.New("request ID already belongs to different ticket inputs")

type Outcome struct {
	RequestID string `json:"request_id"`
	State     string `json:"state"`
	Ticket    Ticket `json:"ticket"`
	LinkID    string `json:"link_id,omitempty"`
}
type receipt struct {
	Version int     `json:"version"`
	Hash    string  `json:"hash"`
	Request Request `json:"request"`
	Outcome Outcome `json:"outcome"`
}
type LinkFunc func(context.Context, Ticket) (api.IssueTrackerLink, error)

// Run holds a cross-process lock for this request and journals dispatch before
// contacting the provider. Keep dir private and retain it for the retry horizon.
// A transport failure or crash after dispatch never authorizes a second create.
func Run(ctx context.Context, dir string, r Request, token, recoveryURL string, provider Provider, link LinkFunc) (Outcome, error) {
	var err error
	r, err = ValidateRequest(r)
	if err != nil {
		return Outcome{}, err
	}
	if provider == nil || link == nil {
		return Outcome{}, errors.New("provider and Gregale linker are required")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return Outcome{}, errors.New("cannot create ticket journal directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return Outcome{}, errors.New("ticket journal directory must be private (0700) and not a symlink")
	}
	lock := flock.New(filepath.Join(dir, r.RequestID+".lock"))
	held, err := lock.TryLock()
	if err != nil {
		return Outcome{}, errors.New("cannot lock ticket journal")
	}
	if !held {
		return Outcome{}, ErrBusy
	}
	defer func() { _ = lock.Unlock() }()
	path := filepath.Join(dir, r.RequestID+".json")
	encoded, _ := json.Marshal(r)
	sum := sha256.Sum256(encoded)
	hash := hex.EncodeToString(sum[:])
	entry := receipt{Version: 1, Hash: hash, Request: r, Outcome: Outcome{RequestID: r.RequestID, State: "ready"}}
	if data, e := readReceipt(path); e == nil {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if dec.Decode(&entry) != nil || dec.Decode(&struct{}{}) != io.EOF || entry.Version != 1 {
			return Outcome{}, errors.New("ticket journal is invalid; preserve it for recovery")
		}
		stored, _ := json.Marshal(entry.Request)
		check := sha256.Sum256(stored)
		if entry.Hash != hash || hex.EncodeToString(check[:]) != entry.Hash || entry.Outcome.RequestID != r.RequestID {
			return Outcome{}, ErrConflict
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return Outcome{}, e
	}
	if recoveryURL != "" && (entry.Outcome.State == "created" || entry.Outcome.State == "linked") {
		n, e := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: recoveryURL})
		if e != nil || n.URL != entry.Outcome.Ticket.URL {
			return entry.Outcome, errors.New("recovery URL conflicts with the stored ticket")
		}
	}
	save := func() error { return writeReceipt(dir, path, entry) }
	switch entry.Outcome.State {
	case "linked":
		if err = validateStoredTicket(r, entry.Outcome.Ticket); err != nil {
			return entry.Outcome, err
		}
		if id, e := uuid.Parse(entry.Outcome.LinkID); e != nil || id == uuid.Nil {
			return entry.Outcome, errors.New("ticket journal has an invalid linked identity")
		}
		return entry.Outcome, nil
	case "created":
		if err = validateStoredTicket(r, entry.Outcome.Ticket); err != nil {
			return entry.Outcome, err
		}
	case "dispatching":
		if recoveryURL == "" {
			return entry.Outcome, ErrOutcomeUnknown
		}
		if err = validateToken(token); err != nil {
			return entry.Outcome, err
		}
		entry.Outcome.Ticket, err = provider.Recover(ctx, r, token, recoveryURL)
		if err != nil {
			return entry.Outcome, err
		}
		if err = validateStoredTicket(r, entry.Outcome.Ticket); err != nil {
			return entry.Outcome, err
		}
		entry.Outcome.State = "created"
		if err = save(); err != nil {
			return entry.Outcome, err
		}
	case "ready":
		if recoveryURL != "" {
			return entry.Outcome, errors.New("recovery applies only to an unconfirmed dispatch")
		}
		if err = validateToken(token); err != nil {
			return entry.Outcome, err
		}
		if err = ctx.Err(); err != nil {
			return entry.Outcome, err
		}
		entry.Outcome.State = "dispatching"
		if err = save(); err != nil {
			return entry.Outcome, err
		}
		ticket, e := provider.Create(ctx, r, token)
		if e != nil {
			var rejected *RejectedError
			if errors.As(e, &rejected) {
				entry.Outcome.State = "ready"
				if err = save(); err != nil {
					return entry.Outcome, err
				}
				return entry.Outcome, e
			}
			return entry.Outcome, fmt.Errorf("%w: %w", ErrOutcomeUnknown, e)
		}
		if err = validateStoredTicket(r, ticket); err != nil {
			return entry.Outcome, fmt.Errorf("%w: %w", ErrOutcomeUnknown, err)
		}
		entry.Outcome.Ticket = ticket
		entry.Outcome.State = "created"
		if err = save(); err != nil {
			return entry.Outcome, err
		}
	default:
		return entry.Outcome, errors.New("ticket journal has an unknown state")
	}
	linked, e := link(ctx, entry.Outcome.Ticket)
	if e != nil {
		return entry.Outcome, errors.New("ticket exists but Gregale linking failed; retry the same request ID and inputs")
	}
	normalized, e := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: linked.URL})
	id, idErr := uuid.Parse(linked.ID)
	if e != nil || normalized.URL != entry.Outcome.Ticket.URL || idErr != nil || id == uuid.Nil {
		return entry.Outcome, errors.New("gregale returned an inconsistent reference; retry linking with the same request")
	}
	entry.Outcome.State = "linked"
	entry.Outcome.LinkID = linked.ID
	if err = save(); err != nil {
		return entry.Outcome, err
	}
	return entry.Outcome, nil
}
func validateStoredTicket(r Request, t Ticket) error {
	n, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: t.URL})
	if err != nil || n.URL != t.URL || n.Provider != r.Provider || t.ProviderID == "" {
		return errors.New("ticket journal contains an invalid provider reference")
	}
	if r.Provider == "github" && n.ExternalKey != r.Target+"#"+t.ProviderID || r.Provider == "linear" && t.ProviderID != r.RequestID {
		return errors.New("ticket journal contains a mismatched provider identity")
	}
	return nil
}
func readReceipt(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > api.IssueTrackerResponseMaxBytes {
		return nil, errors.New("ticket journal file must be private, regular and bounded")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, errors.New("cannot open ticket journal directory")
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, errors.New("cannot read ticket journal")
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Mode().Perm()&0077 != 0 || opened.Size() > api.IssueTrackerResponseMaxBytes {
		return nil, errors.New("ticket journal changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, api.IssueTrackerResponseMaxBytes+1))
	if err != nil || len(data) > api.IssueTrackerResponseMaxBytes {
		return nil, errors.New("cannot read bounded ticket journal")
	}
	return data, nil
}
func writeReceipt(dir, path string, r receipt) error {
	data, err := json.Marshal(r)
	if len(data) > api.IssueTrackerResponseMaxBytes {
		return errors.New("ticket journal exceeds the envelope bound")
	}
	if err != nil {
		return errors.New("cannot encode ticket journal")
	}
	file, err := os.CreateTemp(dir, ".ticket-*")
	if err != nil {
		return errors.New("cannot create ticket journal file")
	}
	temp := file.Name()
	defer func() { _ = os.Remove(temp) }()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return errors.New("cannot write ticket journal")
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("cannot sync ticket journal")
	}
	if err = file.Close(); err != nil {
		return errors.New("cannot close ticket journal")
	}
	if err = os.Rename(temp, path); err != nil {
		return errors.New("cannot publish ticket journal")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errors.New("cannot open ticket journal directory")
	}
	defer func() { _ = root.Close() }()
	directory, err := root.Open(".")
	if err != nil {
		return errors.New("cannot open ticket journal directory")
	}
	defer func() { _ = directory.Close() }()
	if err = directory.Sync(); err != nil {
		return errors.New("cannot sync ticket journal directory")
	}
	return nil
}
