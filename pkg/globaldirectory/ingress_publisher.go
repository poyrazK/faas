package globaldirectory

// adr: 786
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type IngressPublisher struct {
	Store    IngressStore
	Signer   runtimequota.Signer
	Trust    runtimequota.Trust
	Verifier Verifier
	Export   func([]byte) error
	OnError  func()
}

func (p IngressPublisher) valid() bool {
	return p.Store != nil && p.Export != nil && p.Trust.CheckSigner(p.Signer, runtimequota.Directory) == nil
}

func (p IngressPublisher) Run(ctx context.Context) error {
	if !p.valid() {
		return runtimequota.ErrTrust
	}
	ticker := time.NewTicker(api.GlobalIngressPublishInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := p.Once(ctx); err != nil && ctx.Err() == nil && p.OnError != nil {
			p.OnError()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p IngressPublisher) Once(ctx context.Context) error {
	if !p.valid() {
		return runtimequota.ErrTrust
	}
	work, cancel := context.WithTimeout(ctx, api.GlobalIngressPublishTimeout)
	defer cancel()
	catalog, err := p.Store.IngressCatalog(work)
	if err != nil {
		return err
	}
	observed := p.observe(work, catalog)
	if err := work.Err(); err != nil {
		return err
	}
	err = p.Store.CommitIngress(work, catalog, p.Signer.IssuerID(), func(generation int64) (runtimequota.Envelope, error) {
		snapshot := ingressSnapshot(catalog, p.Signer.IssuerID(), generation, observed, time.Now().UTC())
		return p.Signer.SignIngressSnapshot(snapshot)
	})
	if err != nil {
		return err
	}
	// Export rereads the durable latest publication under its producer lock;
	// never export a private envelope after another publisher committed.
	return p.Store.ExportIngress(work, func(raw []byte) error {
		e, err := runtimequota.DecodeIngressSnapshot(raw)
		if err != nil {
			return err
		}
		s, err := p.Trust.VerifyIngressSnapshot(e, p.Signer.IssuerID())
		if err != nil {
			return err
		}
		now := time.Now().UnixMicro()
		if s.IssuedAtMicros > now || s.ExpiresAtMicros <= now {
			return ErrIngressChanged
		}
		return p.Export(raw)
	})
}

func (p IngressPublisher) observe(ctx context.Context, c IngressCatalog) map[string]runtimequota.ServingStatus {
	jobs := make(chan IngressCandidate)
	out := map[string]runtimequota.ServingStatus{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range api.GlobalIngressProbeConcurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range jobs {
				status, err := p.probe(ctx, v)
				if err == nil {
					mu.Lock()
					out[ingressCandidateKey(v)] = status
					mu.Unlock()
				}
			}
		}()
	}
send:
	for _, app := range c {
		for _, v := range app.Candidates {
			select {
			case jobs <- v:
			case <-ctx.Done():
				break send
			}
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

func (p IngressPublisher) probe(ctx context.Context, v IngressCandidate) (runtimequota.ServingStatus, error) {
	now := time.Now().UTC()
	challenge := runtimequota.ServingProbe{Nonce: uuid.NewString(), ConsentID: v.Consent.ID, Grant: v.Grant, Origin: v.Origin, IssuedAtMicros: now.UnixMicro(), ExpiresAtMicros: min(now.Add(api.GlobalIngressLeaseDuration).UnixMicro(), v.Consent.ExpiresAtMicros)}
	e, err := p.Signer.SignServingProbe(challenge)
	if err != nil {
		return runtimequota.ServingStatus{}, ErrVerification
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return runtimequota.ServingStatus{}, ErrVerification
	}
	ctx, cancel := context.WithTimeout(ctx, api.GlobalIngressProbeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, v.Grant.Binding.APIBase+"/internal/v1/ingress-status", bytes.NewReader(raw))
	if err != nil {
		return runtimequota.ServingStatus{}, ErrVerification
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.Verifier.probeClient().Do(request)
	if err != nil {
		return runtimequota.ServingStatus{}, ErrVerification
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return runtimequota.ServingStatus{}, ErrVerification
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.GlobalIngressProbeMaxBytes+1))
	if err != nil {
		return runtimequota.ServingStatus{}, ErrVerification
	}
	status, err := runtimequota.DecodeServingStatus(body, challenge)
	now = time.Now().UTC()
	if err != nil || status.ObservedAtMicros > now.UnixMicro() || status.ExpiresAtMicros <= now.UnixMicro() {
		return runtimequota.ServingStatus{}, ErrVerification
	}
	return status, nil
}
