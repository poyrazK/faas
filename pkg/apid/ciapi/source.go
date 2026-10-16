package ciapi

import (
	"context"
	"io"
	"sync"

	"github.com/onebox-faas/faas/pkg/ci"
)

// RPCSource adapts githubd's streamed RPC, whose final metadata is available
// only after Body.Close. EOF is withheld until its exact-SHA receipt is checked.
// This prevents a partial stream or missing trailer from becoming a source blob.
type RPCSource struct {
	Open func(context.Context, string, int64, string, string, int64) (io.ReadCloser, func() (string, bool, error), error)
}

func (s RPCSource) StreamSourceRef(ctx context.Context, account string, installation int64, repository, sha string, bound int64) (io.ReadCloser, string, bool, int64, error) {
	body, receipt, err := s.Open(ctx, account, installation, repository, sha, bound)
	if err != nil {
		return nil, "", false, 0, ci.ErrGitHubUnavailable
	}
	if body == nil || receipt == nil {
		if body != nil {
			_ = body.Close()
		}
		return nil, "", false, 0, ci.ErrGitHubUnavailable
	}
	return &sourceReceipt{ReadCloser: body, receipt: receipt, sha: sha}, sha, false, 0, nil
}

type sourceReceipt struct {
	io.ReadCloser
	receipt  func() (string, bool, error)
	sha      string
	once     sync.Once
	closeErr error
}

func (b *sourceReceipt) Close() error {
	b.once.Do(func() { b.closeErr = b.ReadCloser.Close() })
	return b.closeErr
}

func (b *sourceReceipt) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		closeErr := b.Close()
		sha, truncated, receiptErr := b.receipt()
		if closeErr != nil || receiptErr != nil || truncated || sha != b.sha {
			return n, ci.ErrGitHubUnavailable
		}
	}
	return n, err
}
