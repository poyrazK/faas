package internalmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// ExportMonitoring publishes only aggregate mail telemetry through the existing
// node_exporter textfile collector. It adds no TCP listener. A failed collection
// replaces old healthy samples with an explicit failure and a fresh attempt time.
func (b *PostfixBridge) ExportMonitoring(ctx context.Context, output string) error {
	if !b.enabled() {
		return ErrDisabled
	}
	if !filepath.IsAbs(output) || filepath.Clean(output) != output || filepath.Ext(output) != ".prom" {
		return ErrInvalid
	}
	healthOn := b.relayHealthEnabled()
	var queues RelayQueues
	var queueErr, healthErr error
	if healthOn {
		queues, queueErr, healthErr = b.collectRelayHealth(ctx)
	}
	if !b.enabled() {
		return ErrDisabled
	}
	body, err := b.readMonitoring(ctx)
	up := 1
	if err != nil {
		up = 0
		body = nil
	} else {
		if !b.enabled() {
			return ErrDisabled
		}
		if !healthOn {
			queues, queueErr = b.queues(ctx)
		}
		body = appendRelayMetrics(body, queues, queueErr)
	}
	if healthErr != nil {
		up = 0
	}
	body = append(body, []byte(fmt.Sprintf("\n# TYPE maild_mail_monitoring_up gauge\nmaild_mail_monitoring_up %d\n# TYPE maild_mail_monitoring_collected_at_seconds gauge\nmaild_mail_monitoring_collected_at_seconds %d\n", up, time.Now().Unix()))...)
	if !b.enabled() {
		return ErrDisabled
	}
	if writeErr := writeMonitoring(output, body); writeErr != nil {
		return writeErr
	}
	return errors.Join(err, healthErr)
}

func (b *PostfixBridge) readMonitoring(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://maild"+MonitoringPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("Accept", "text/plain; version=0.0.4")
	response, err := b.client.Do(req)
	if err != nil {
		return nil, errors.New("internal mail: monitoring unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("internal mail: monitoring request rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.InternalMailMaxMonitoringBytes+1))
	if err != nil || len(body) > api.InternalMailMaxMonitoringBytes {
		return nil, ErrInvalid
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil || families["maild_mail_enabled"] == nil || families["maild_mail_stats_up"] == nil {
		return nil, ErrInvalid
	}
	for name := range families {
		if !strings.HasPrefix(name, "maild_mail_") || strings.HasPrefix(name, "maild_mail_monitoring_") || strings.HasPrefix(name, "maild_mail_postfix_") {
			return nil, ErrInvalid
		}
	}
	return body, nil
}

func appendRelayMetrics(body []byte, queues RelayQueues, err error) []byte {
	body = append(body, []byte(fmt.Sprintf("\n# TYPE maild_mail_postfix_stats_up gauge\nmaild_mail_postfix_stats_up %g\n", boolMetric(err == nil)))...)
	if err != nil {
		return body
	}
	body = append(body, []byte("# TYPE maild_mail_postfix_queue_messages gauge\n# TYPE maild_mail_postfix_oldest_seconds gauge\n")...)
	for _, item := range []struct {
		kind string
		stat StateStat
	}{{"delivery", queues.Delivery}, {"report", queues.Reports}} {
		body = append(body, []byte(fmt.Sprintf("maild_mail_postfix_queue_messages{kind=%q} %d\nmaild_mail_postfix_oldest_seconds{kind=%q} %g\n", item.kind, item.stat.Count, item.kind, item.stat.OldestSeconds))...)
	}
	return body
}

func writeMonitoring(path string, body []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".mail-metrics-*")
	if err != nil {
		return errors.New("internal mail: cannot create monitoring file")
	}
	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	// Only aggregate telemetry is published. node_exporter reads this file
	// under a different UID/group in its shared collector directory.
	if err := file.Chmod(0644); err != nil { // #nosec G302 -- public aggregate metrics, no mail or credentials
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
