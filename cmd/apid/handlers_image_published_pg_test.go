//go:build !no_pg

// adr: 679
package main

import (
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestImagePublishedConcurrentPostgresDelivery(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	app := seedPublishedImageApp(t, e.store, e.acct.ID, "published-concurrent")
	httpServer := httptest.NewServer(e.h)
	defer httpServer.Close()
	client := api.NewClient(httpServer.URL, e.key)
	const deliveries = 8
	var wait sync.WaitGroup
	results := make(chan api.DeploymentResponse, deliveries)
	failures := make(chan error, deliveries)
	for range deliveries {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := client.PublishAppImage(t.Context(), app.Slug, api.CreateDeploymentRequest{Image: publishedTestImage("a")})
			if err != nil {
				failures <- err
				return
			}
			results <- response
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var id string
	for response := range results {
		if id == "" {
			id = response.ID
		}
		if response.ID != id {
			t.Fatalf("concurrent publisher returned different deployments: %s and %s", id, response.ID)
		}
	}
	rows, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
	if err != nil || len(rows) != 1 || id == "" {
		t.Fatalf("durable image deliveries = %d, %v; id=%s", len(rows), err, id)
	}
}
