//go:build linux && metal

// spec: §14.A
// adr: 1530
package m9

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

type fixtureReceipt struct {
	FixtureID        string `json:"fixture_id"`
	BootID           string `json:"boot_id"`
	OperationID      string `json:"operation_id"`
	Sequence         int    `json:"sequence"`
	UniqueOperations int    `json:"unique_operations"`
}

// probe uses the real vmmd HTTP bridge into the named guest. It deliberately
// does not qualify gateway routing, network partitions or application databases.
func probe(ctx context.Context, p *nativePeer, instance, fixture, operation string) (fixtureReceipt, error) {
	var receipt fixtureReceipt
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	payload, err := json.Marshal(map[string]string{"fixture_id": fixture, "operation_id": operation})
	if err != nil {
		return receipt, err
	}
	stream, err := p.client.ForwardHTTPStream(callCtx)
	if err != nil {
		return receipt, err
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{
		Instance: instance, Method: "POST", RequestUri: "/probe", Port: 8080, AppProtocol: "http1",
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}, {Name: "Content-Length", Value: strconv.Itoa(len(payload))}},
	}}}); err != nil {
		return receipt, err
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_BodyChunk{BodyChunk: payload}}); err != nil {
		return receipt, err
	}
	if err := stream.CloseSend(); err != nil {
		return receipt, err
	}
	var body []byte
	headers := false
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return receipt, err
		}
		if init := frame.GetInit(); init != nil {
			if headers || init.GetStatus() != 200 || init.GetError() != "" {
				return receipt, errors.New("native M9 fixture did not return a successful, single HTTP response")
			}
			headers = true
		} else {
			if !headers {
				return receipt, errors.New("native M9 fixture body arrived before its HTTP status")
			}
			body = append(body, frame.GetBodyChunk()...)
			if len(body) > 4096 {
				return receipt, errors.New("native M9 fixture response exceeds 4 KiB")
			}
		}
	}
	if !headers || json.Unmarshal(body, &receipt) != nil ||
		receipt.FixtureID != fixture || receipt.OperationID != operation ||
		!CanonicalUUID(receipt.BootID) || receipt.Sequence <= 0 || receipt.UniqueOperations < receipt.Sequence {
		return receipt, errors.New("native M9 fixture returned an invalid or unrelated operation receipt")
	}
	return receipt, nil
}
