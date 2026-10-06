package routeimpact

import (
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func requireNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for the Express and Hono parser integration tests")
	}
}

func TestNodeHTTPAutoDetectionAndFingerprint(t *testing.T) {
	snapshot := testSnapshot(map[string]string{"src/app.ts": `const api = "routes";`, "package.json": `{"name":"api"}`})
	if got := detectFramework(snapshot, sourceSnapshot{}); got != "node-http" {
		t.Fatalf("auto framework = %q", got)
	}
	fingerprintForFramework(&snapshot, "node-http")
	if snapshot.meta.JavaScriptFiles != 1 || snapshot.meta.PythonFiles != 0 || snapshot.meta.GoFiles != 0 {
		t.Fatalf("Node source counts: %+v", snapshot.meta)
	}
}

func indexNodeFixture(t *testing.T, sources map[string]string) sourceIndex {
	t.Helper()
	requireNode(t)
	snapshot := testSnapshot(sources)
	fingerprintForFramework(&snapshot, "node-http")
	index, err := indexSource(context.Background(), snapshot, "node-http", "")
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestNodeHTTPExpressMountedRouterAndImportedHandler(t *testing.T) {
	index := indexNodeFixture(t, map[string]string{
		"src/main.ts":     `import express from "express"; import { apiRouter } from "./routes"; const app = express(); app.use("/api", apiRouter); const dynamicPath = process.env.PATH || "/x"; app.get(dynamicPath, showUser);`,
		"src/routes.ts":   `import { Router } from "express"; import { requireUser } from "./auth"; import { showUser } from "./handlers"; export const apiRouter = Router(); apiRouter.get("/users/:id", requireUser, showUser); apiRouter.route("/teams").get(showUser); export function registerRoutes(target: any) { target.get("/hidden", showUser); }`,
		"src/auth.ts":     `export function requireUser(req: unknown, res: unknown, next: () => void) { next(); }`,
		"src/handlers.ts": `export function showUser(req: unknown, res: unknown, callback: () => void) { return callback(); }`,
	})
	issueCodes := map[string]bool{}
	for _, issue := range index.Issues {
		issueCodes[issue.Code] = true
	}
	if len(index.Issues) != 2 || !issueCodes["dynamic_route_path"] || !issueCodes["unresolved_route_receiver"] || len(index.Routes) != 2 {
		t.Fatalf("routes=%+v issues=%+v", index.Routes, index.Issues)
	}
	var route Route
	for _, candidate := range index.Routes {
		if candidate.Path == "/api/users/{id}" {
			route = candidate
		}
	}
	if route.Method != "GET" || route.Path != "/api/users/{id}" || route.Handler != "showUser" || route.Source.File != "src/handlers.ts" || route.Registration.File != "src/routes.ts" {
		t.Fatalf("mounted imported handler: %+v", route)
	}
	if !reflect.DeepEqual(route.ContextFiles, []string{"src/main.ts", "src/routes.ts"}) {
		t.Fatalf("route context: %+v", route.ContextFiles)
	}
	if !reflect.DeepEqual(index.Dependencies["src/routes.ts"], []string{"src/auth.ts", "src/handlers.ts"}) {
		t.Fatalf("local imports: %+v", index.Dependencies)
	}
	if len(route.DependencySymbols) != 1 || route.DependencySymbols[0] != "src/auth.ts#requireUser" {
		t.Fatalf("route middleware roots: %+v", route.DependencySymbols)
	}
	if got := index.Symbols["src/handlers.ts#showUser"].Issues; len(got) != 1 || got[0].Code != "unresolved_node_call" {
		t.Fatalf("unresolved handler callback was not explicit: %+v", got)
	}
}

func TestNodeHTTPHonoMountAndPathParameters(t *testing.T) {
	index := indexNodeFixture(t, map[string]string{
		"src/app.ts": `import { OpenAPIHono, createRoute } from "@hono/zod-openapi"; const app = new OpenAPIHono(); const orders = new OpenAPIHono(); orders.post("/orders/:orderID", createOrder); const getOrderRoute = createRoute({ method: "get", path: "/orders/{id}", responses: {} }); orders.openapi(getOrderRoute, getOrder); app.route("/v1", orders); function createOrder(c: unknown) { return c; } function getOrder(c: unknown) { return c; }`,
	})
	if len(index.Issues) != 0 || len(index.Routes) != 2 {
		t.Fatalf("routes=%+v issues=%+v", index.Routes, index.Issues)
	}
	paths := map[string]bool{}
	for _, route := range index.Routes {
		paths[route.Path] = true
	}
	if !paths["/v1/orders/{orderID}"] || !paths["/v1/orders/{id}"] {
		t.Fatalf("effective Hono paths = %+v", paths)
	}
}

func TestAnalyzeAutoDetectsNodeAndTracksImportedFunctionChanges(t *testing.T) {
	requireNode(t)
	dir := t.TempDir()
	gitFixture(t, dir, "init", "-q")
	writeFixture(t, dir, "src/app.js", `throw new Error("customer code must not execute"); import express from "express"; import { format } from "./format.js"; const app = express(); app.get("/items", handler); function handler() { return format("a"); }`)
	writeFixture(t, dir, "src/format.js", `export function format(value) { return value.toUpperCase(); }`)
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-qm", "Node baseline")
	writeFixture(t, dir, "src/format.js", `export function format(value) { return value.toLowerCase(); }`)
	report, err := Analyze(context.Background(), Options{Path: dir, Base: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Framework != "node-http" || report.Version != 4 || report.Status != "complete" || report.Summary.PotentiallyAffected != 1 || report.Routes[0].Change != "potentially_affected" || report.Base.JavaScriptFiles != 2 || report.Candidate.JavaScriptFiles != 2 {
		t.Fatalf("unexpected impact: %+v", report)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "customer code must not execute") {
		t.Fatal("route report leaked or executed customer source")
	}
	if _, err := ParseReport(body); err != nil {
		t.Fatalf("version 4 report did not validate: %v", err)
	}
}
