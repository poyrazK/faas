package objectqualification

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"golang.org/x/time/rate"
)

type expectedObject struct {
	Size         int64
	SHA256, ETag string
}
type expectedReceipt struct {
	ID, Key, ETag string
	Size          int64
	Rejected      bool
}
type objectModel struct {
	mu       sync.Mutex
	objects  map[string]*expectedObject
	receipts []expectedReceipt
}

func (m *objectModel) set(key string, value *expectedObject) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = value
}
func (m *objectModel) remember(value expectedReceipt) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receipts = append(m.receipts, value)
}

func payload(seed int64, identity string, size int64) []byte {
	block := sha256.Sum256([]byte(strconv.FormatInt(seed, 10) + ":" + identity))
	body := make([]byte, size)
	for offset := 0; offset < len(body); {
		offset += copy(body[offset:], block[:])
	}
	return body
}

func expectation(body []byte, etag string) *expectedObject {
	sum := sha256.Sum256(body)
	return &expectedObject{Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), ETag: etag}
}

func responseReceipt(out *s3.PutObjectOutput, err error) string {
	if out != nil {
		if response, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response); ok && response != nil {
			return response.Header.Get("X-Gregale-Upload-ID")
		}
	}
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) {
		return response.HTTPResponse().Header.Get("X-Gregale-Upload-ID")
	}
	return ""
}

func apiCode(err error, code string) bool {
	var apiError smithy.APIError
	return errors.As(err, &apiError) && apiError.ErrorCode() == code
}

func (l *lab) put(ctx context.Context, key string, body []byte, reject bool, m *measurements) (string, error) {
	l.traffic.RLock()
	defer l.traffic.RUnlock()
	input := &s3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String(key), Body: bytes.NewReader(body), ContentType: aws.String("application/octet-stream")}
	if reject {
		input.IfNoneMatch = aws.String("*")
	}
	started := time.Now()
	out, err := l.gateway.client.PutObject(ctx, input)
	id := responseReceipt(out, err)
	expectedRejection := reject && apiCode(err, "PreconditionFailed")
	resultErr := err
	if expectedRejection {
		resultErr = nil
	}
	if reject && err == nil {
		resultErr = errors.New("conditional write unexpectedly succeeded")
	}
	if resultErr == nil && id == "" {
		resultErr = errors.New("gateway response omitted its upload receipt")
	}
	if m != nil {
		m.record("put", started, int64(len(body)), expectedRejection, resultErr)
	}
	if resultErr != nil {
		return id, resultErr
	}
	etag := ""
	if out != nil {
		etag = aws.ToString(out.ETag)
	}
	l.model.remember(expectedReceipt{ID: id, Key: key, ETag: etag, Size: int64(len(body)), Rejected: reject})
	if !reject {
		l.model.set(key, expectation(body, etag))
	}
	return id, nil
}

func (l *lab) get(ctx context.Context, key string, want *expectedObject, m *measurements) error {
	started := time.Now()
	out, err := l.gateway.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(key)})
	missing := want == nil && apiCode(err, "NoSuchKey")
	if missing {
		if m != nil {
			m.record("get", started, 0, true, nil)
		}
		return nil
	}
	if err == nil {
		defer func() { _ = out.Body.Close() }()
		if want == nil {
			err = errors.New("deleted object was resurrected")
		} else {
			h := sha256.New()
			n, readErr := io.Copy(h, io.LimitReader(out.Body, want.Size+1))
			if readErr != nil {
				err = readErr
			} else if n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256 || aws.ToString(out.ETag) != want.ETag {
				err = errors.New("GET bytes, length or ETag differ from acknowledged data")
			}
		}
	}
	if m != nil {
		size := int64(0)
		if want != nil {
			size = want.Size
		}
		m.record("get", started, size, false, err)
	}
	return err
}

func (l *lab) delete(ctx context.Context, key string, m *measurements) error {
	l.traffic.Lock()
	defer l.traffic.Unlock()
	started := time.Now()
	_, err := l.gateway.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(key)})
	if m != nil {
		m.record("delete", started, 0, false, err)
	}
	if err == nil {
		l.model.set(key, nil)
	}
	return err
}

func (l *lab) mixedWorkflow(ctx context.Context, worker, iteration int, m *measurements) error {
	key := fmt.Sprintf("worker-%d/object", worker)
	identity := fmt.Sprintf("%d/%d", worker, iteration)
	body := payload(l.options.Seed, identity, l.options.ObjectBytes)
	if iteration%3 == 0 {
		body = nil
	}
	if _, err := l.put(ctx, key, body, false, m); err != nil {
		return err
	}
	l.model.mu.Lock()
	want := *l.model.objects[key]
	l.model.mu.Unlock()
	if err := l.get(ctx, key, &want, m); err != nil {
		return err
	}
	body = payload(l.options.Seed, identity+"-overwrite", l.options.ObjectBytes)
	if _, err := l.put(ctx, key, body, false, m); err != nil {
		return err
	}
	if _, err := l.put(ctx, key, body, true, m); err != nil {
		return err
	}
	if err := l.delete(ctx, key, m); err != nil {
		return err
	}
	if err := l.get(ctx, key, nil, m); err != nil {
		return err
	}
	if iteration%4 == 0 {
		return l.multipart(ctx, fmt.Sprintf("worker-%d/multipart", worker), identity, m)
	}
	if iteration%4 == 1 {
		return l.abortMultipart(ctx, fmt.Sprintf("worker-%d/aborted", worker), identity, m)
	}
	return nil
}

func (l *lab) multipart(ctx context.Context, key, identity string, m *measurements) (err error) {
	l.traffic.RLock()
	defer l.traffic.RUnlock()
	started := time.Now()
	created, err := l.gateway.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String(key)})
	m.record("multipart_create", started, 0, false, err)
	if err != nil {
		return err
	}
	parts := []types.CompletedPart{}
	h := sha256.New()
	var size int64
	for i, n := range []int64{api.MinMultipartPartBytes, l.options.ObjectBytes} {
		body := payload(l.options.Seed, identity+"-part-"+strconv.Itoa(i), n)
		_, _ = h.Write(body)
		size += n
		started = time.Now()
		out, uploadErr := l.gateway.client.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String("assets"), Key: aws.String(key), UploadId: created.UploadId, PartNumber: aws.Int32(int32(i + 1)), Body: bytes.NewReader(body)})
		m.record("multipart_part", started, n, false, uploadErr)
		if uploadErr != nil {
			return uploadErr
		}
		parts = append(parts, types.CompletedPart{PartNumber: aws.Int32(int32(i + 1)), ETag: out.ETag})
	}
	started = time.Now()
	completed, err := l.gateway.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String(key), UploadId: created.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}})
	m.record("multipart_complete", started, 0, false, err)
	if err != nil {
		return err
	}
	want := &expectedObject{Size: size, SHA256: hex.EncodeToString(h.Sum(nil)), ETag: aws.ToString(completed.ETag)}
	l.model.set(key, want)
	return l.get(ctx, key, want, m)
}

func (l *lab) load(ctx context.Context, cycle int) (LoadReport, error) {
	if l.options.Soak {
		if err := l.waitSoakSample(ctx); err != nil {
			return LoadReport{Cycle: cycle, StopReason: "failure"}, err
		}
	}
	started := time.Now()
	until := started.Add(l.options.Duration)
	var assigned atomic.Int64
	counts := &loadCounts{}
	measure := &measurements{}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	admission, stopAdmission := context.WithDeadline(work, until)
	defer stopAdmission()
	var pacing *rate.Limiter
	if l.options.Soak && l.options.SoakWorkflowsPerSecond > 0 {
		pacing = rate.NewLimiter(rate.Limit(l.options.SoakWorkflowsPerSecond), 1)
	}
	var monitor *soakMonitor
	if l.options.Soak {
		var err error
		monitor, err = l.startSoak(ctx, cycle, started, measure, counts, cancel)
		if err != nil {
			return measure.finish(cycle, started, 0, "failure"), err
		}
	}
	finish := func(reason string, loadErr error) (LoadReport, error) {
		var soak *SoakReport
		if monitor != nil {
			var err error
			soak, err = monitor.finish()
			loadErr = errors.Join(loadErr, err)
			if err != nil && reason != "cancelled" {
				reason = "failure"
			}
			soak.FullDuration = reason == "duration" && time.Since(started) >= l.options.Duration
		}
		r := measure.finish(cycle, started, counts.completed.Load(), reason)
		r.WorkflowsStarted, r.Soak = counts.started.Load(), soak
		return r, loadErr
	}
	failures := make(chan error, l.options.Workers)
	var workers sync.WaitGroup
	for worker := range l.options.Workers {
		workers.Go(func() {
			for time.Now().Before(until) && work.Err() == nil {
				iteration := int(assigned.Add(1)) - 1
				if iteration >= l.options.MaxWorkflows {
					return
				}
				if pacing != nil && !waitSoakPacing(admission, pacing) {
					return
				}
				if l.options.Soak {
					if err := l.checkSoakAdmission(); err != nil {
						failures <- err
						cancel()
						return
					}
				} else if l.diskBytes.Load() > l.options.DiskBudgetBytes {
					failures <- errors.New("qualification disk budget exceeded; stop and inspect retained history")
					cancel()
					return
				}
				counts.started.Add(1)
				step, stop := context.WithTimeout(work, api.ObjectQualificationRequestTimeout)
				err := l.mixedWorkflow(step, worker, cycle*l.options.MaxWorkflows+iteration, measure)
				stop()
				if err != nil {
					failures <- err
					cancel()
					return
				}
				counts.completed.Add(1)
			}
		})
	}
	workers.Wait()
	close(failures)
	reason := "duration"
	if counts.completed.Load() >= int64(l.options.MaxWorkflows) {
		reason = "workflow_limit"
	}
	for err := range failures {
		return finish("failure", err)
	}
	if ctx.Err() != nil {
		return finish("cancelled", ctx.Err())
	}
	if counts.completed.Load() == 0 {
		return finish("failure", errors.New("load phase completed no workflows"))
	}
	return finish(reason, nil)
}

func waitSoakPacing(ctx context.Context, limiter *rate.Limiter) bool {
	if err := limiter.Wait(ctx); err != nil {
		// Wait can reject a future token before the admission deadline arrives.
		// Keep the requested measurement window open through that final paced gap.
		<-ctx.Done()
		return false
	}
	return ctx.Err() == nil
}

func (l *lab) verify(ctx context.Context) error {
	var bytes, keys int64
	for key, want := range l.model.objects {
		if err := l.get(ctx, key, want, nil); err != nil {
			return fmt.Errorf("verify %s: %w", key, err)
		}
		if want != nil {
			bytes += want.Size
			keys++
		}
	}
	backend, err := l.gateway.registry.Default("us-east-1")
	if err != nil {
		return err
	}
	confirmer := backend.Provider.(objectstorage.ObjectWriteConfirmer)
	for _, receipt := range l.model.receipts {
		row, err := l.gateway.receipt(ctx, receipt.ID)
		status := "completed"
		if receipt.Rejected {
			status = "failed"
		}
		if err != nil || row.Status != status {
			return errors.New("gateway receipt settlement differs from its acknowledged outcome")
		}
		proof, err := confirmer.ConfirmTrackedObject(ctx, "physical", receipt.Key, receipt.ID, receipt.Size)
		if receipt.Rejected {
			if !errors.Is(err, objectstorage.ErrWriteRejected) {
				return errors.New("historical rejection proof was lost")
			}
		} else if err != nil || proof.ETag != receipt.ETag {
			return errors.New("historical committed receipt changed or disappeared")
		}
	}
	return l.gateway.inventory(ctx, bytes, keys)
}

func (l *lab) modelTotals() (bytes, keys int64) {
	for _, want := range l.model.objects {
		if want != nil {
			bytes += want.Size
			keys++
		}
	}
	return bytes, keys
}

func (l *lab) abortMultipart(ctx context.Context, key, identity string, m *measurements) error {
	l.traffic.RLock()
	defer l.traffic.RUnlock()
	started := time.Now()
	u, err := l.gateway.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String(key)})
	m.record("multipart_create", started, 0, false, err)
	if err != nil {
		return err
	}
	body := payload(l.options.Seed, identity+"-abort", l.options.ObjectBytes)
	started = time.Now()
	_, err = l.gateway.client.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String("assets"), Key: aws.String(key), UploadId: u.UploadId, PartNumber: aws.Int32(1), Body: bytes.NewReader(body)})
	m.record("multipart_part", started, int64(len(body)), false, err)
	if err != nil {
		return err
	}
	started = time.Now()
	_, err = l.gateway.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String(key), UploadId: u.UploadId})
	m.record("multipart_abort", started, 0, false, err)
	if err == nil {
		l.model.set(key, nil)
	}
	return err
}
