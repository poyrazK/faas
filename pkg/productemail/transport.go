package productemail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/mail"
)

// Transport deliberately excludes log/noop and non-idempotent senders. A
// successful provider call is acceptance, never proof of inbox delivery.
type Transport struct {
	Mode      string
	sender    mail.Sender
	directory string
}

type Receipt struct{ Status, ProviderID string }

func (t *Transport) SendWithReceipt(ctx context.Context, id string, msg mail.Message) (Receipt, error) {
	if t != nil && t.Mode == "resend" {
		sender, ok := t.sender.(interface {
			SendWithReceipt(context.Context, mail.Message) (string, error)
		})
		if !ok {
			return Receipt{}, errors.New("product email receipt transport unavailable")
		}
		providerID, err := sender.SendWithReceipt(ctx, msg)
		return Receipt{Status: "provider_accepted", ProviderID: providerID}, err
	}
	status, err := t.Send(ctx, id, msg)
	return Receipt{Status: status}, err
}

func NewResend(config mail.ResendConfig) (*Transport, error) {
	sender, err := mail.NewResendSender(config)
	if err != nil {
		return nil, err
	}
	return &Transport{Mode: "resend", sender: sender}, nil
}
func NewLocal(directory string) (*Transport, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("product email sink requires an absolute path")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return nil, errors.New("product email sink must be a private directory (0700)")
	}
	return &Transport{Mode: "local", directory: directory}, nil
}
func (t *Transport) Send(ctx context.Context, id string, msg mail.Message) (string, error) {
	if t == nil {
		return "", errors.New("product email transport disabled")
	}
	if t.Mode == "resend" && t.sender != nil {
		return "provider_accepted", t.sender.Send(ctx, msg)
	}
	if t.Mode != "local" || t.directory == "" {
		return "", errors.New("product email transport unavailable")
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	target := filepath.Join(t.directory, id+".json")
	if old, e := os.ReadFile(target); e == nil {
		if !bytes.Equal(old, data) {
			return "", errors.New("product email local delivery changed")
		}
		return "captured", nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	f, err := os.CreateTemp(t.directory, ".product-email-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Rename(f.Name(), target); err != nil {
		return "", fmt.Errorf("capture product email: %w", err)
	}
	return "captured", nil
}
