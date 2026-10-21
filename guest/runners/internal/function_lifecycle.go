package internal

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
)

// Qualified function hooks visit every loaded interpreter. The host owns a
// checkpoint/boot flight and the before-checkpoint group also owns one lease
// until every worker finished its callback or cancellation cleanup.
func serveFunctionLifecycle(w http.ResponseWriter, r *http.Request, cfg ServeConfig) {
	env := requestEnvelope(r, cfg)
	env.Background, env.ForbidBackground = nil, true
	env.ResourceLifecycle = true
	ctx, cancel := context.WithTimeout(r.Context(), invocationTimeout(r.Header))
	defer cancel()
	batch, err := workerpool.OpenAllIfSupported(ctx, cfg.Spec, env)
	if batch == nil {
		cfg.executionLease.ReleaseAfter(ctx, nil)
		if errors.Is(err, workerpool.ErrCompletionUnsupported) {
			http.Error(w, "function adapter does not support lifecycle completion", http.StatusNotImplemented)
			return
		}
		http.Error(w, "function workers have not quiesced", http.StatusServiceUnavailable)
		return
	}
	healthy := make([]bool, len(batch.Lines))
	defer func() { batch.Close(healthy); cfg.executionLease.ReleaseAfter(ctx, batch.Released()) }()
	if err == nil {
		for i, lines := range batch.Lines {
			err = drainFunctionLifecycle(ctx, lines)
			if err != nil {
				break
			}
			healthy[i] = true
		}
	}
	if err != nil {
		http.Error(w, "function lifecycle callback failed", http.StatusServiceUnavailable)
		return
	}
	if err := finishFunctionLifecycle(ctx, cfg, batch, healthy); err != nil {
		http.Error(w, "function lifecycle completion unconfirmed", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func finishFunctionLifecycle(ctx context.Context, cfg ServeConfig, batch *workerpool.Batch, healthy []bool) error {
	batch.Close(healthy)
	select {
	case <-batch.Released():
	case <-ctx.Done():
		return ctx.Err()
	}
	cfg.executionLease.ReleaseAfter(ctx, batch.Released())
	return cfg.executionLease.Wait(ctx)
}

func drainFunctionLifecycle(ctx context.Context, lines *workerpool.Lines) error {
	line, err := lines.NextContext(ctx)
	if err != nil {
		return err
	}
	first, err := decodeResponse(line)
	if err != nil {
		return err
	}
	if first.Status < 200 || first.Status >= 300 {
		return errors.New("runner: lifecycle callback rejected")
	}
	if first.Frame == "" {
		return nil
	}
	if first.Frame != FrameHead {
		return errors.New("runner: invalid lifecycle response")
	}
	for {
		line, err := lines.NextContext(ctx)
		if err != nil {
			return err
		}
		frame, err := decodeResponse(line)
		if err != nil {
			return err
		}
		switch frame.Frame {
		case FrameEnd:
			return nil
		case FrameChunk:
		default:
			return errors.New("runner: invalid lifecycle stream")
		}
	}
}
