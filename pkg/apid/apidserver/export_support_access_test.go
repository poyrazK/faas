package apidserver

import "context"

// SummarizeSupportAccess runs one pass of the end-of-access summary loop.
func (r *server) SummarizeSupportAccess(ctx context.Context) (int, error) {
	return r.supportAccessService().SummarizeEnded(ctx)
}
