# Gregale API-hosting capability matrix

<!-- GENERATED — do not edit by hand; regenerate with `make capabilities-md`. -->

This matrix is generated from [`pkg/productcap/catalog.json`](../pkg/productcap/catalog.json). Maturity is the product promise: `internal` is operator-only, `preview` is usable with explicit qualification, `beta` is supported for production-shaped use, and `ga` is generally available.

| Capability | Category | Maturity | Plans | Description | Acceptance evidence |
|---|---|---|---|---|---|
| [Custom domains](https://gregale.dev/docs/custom-domains) | edge | `beta` | `free`, `hobby`, `pro`, `scale` | Serve an API on a customer-owned domain with managed TLS. | `pkg/gateway/allowlist_test.go::TestOnDemandAllowlist_CustomDomainTakesPrecedence` |
| [Firecracker isolation](https://gregale.dev/docs/security) | runtime | `beta` | `free`, `hobby`, `pro`, `scale` | Run each workload in an isolated Firecracker microVM. | `cmd/e2e/source_deploy_wake_metal_test.go::TestSourceDeployWakeMetal` |
| [GitHub deployments](https://gregale.dev/docs/deploy-from-github) | delivery | `beta` | `free`, `hobby`, `pro`, `scale` | Deploy an API from a connected GitHub repository and ref. | `pkg/githubdgrpc/handlers_round_trip_test.go::TestCreateDeploymentFromPush_HappyPath` |
| [Managed PostgreSQL](../docs/managed-postgres) | data | `internal` | — | Provision and bind a managed PostgreSQL database to an API. | `pkg/managedpostgres/service_test.go::TestCreateIsIdempotentAndPersistsPlacement` |
| [Private object storage](../docs/object-storage) | data | `preview` | `hobby`, `pro`, `scale` | Use private S3-backed buckets and signed URLs from an API. | `pkg/objectstorage/s3_test.go::TestS3ProtocolAndErrors` |
| [Pull-request previews](../docs/preview-environments) | delivery | `beta` | `free`, `hobby`, `pro`, `scale` | Create an isolated, reviewable API URL for a pull request. | `pkg/dashboard/preview_panel_test.go::TestRender_AppDetail_PreviewPanel_Shape` |
| [Scale to zero](https://gregale.dev/docs/scale-to-zero) | runtime | `beta` | `free`, `hobby`, `pro`, `scale` | Park idle APIs and wake them on the next request. | `pkg/gateway/handler_test.go::TestColdWakeReturns200AndHeader` |
| [Source deploys](https://gregale.dev/docs/deploy-from-source) | delivery | `beta` | `free`, `hobby`, `pro`, `scale` | Deploy an API from a local source tree, tarball, Dockerfile, or OCI image. | `cmd/e2e/apply_project_e2e_test.go::TestApplyProject_DeploymentKindTarball` |
| [Streaming and gRPC](https://gregale.dev/docs/runtime-node) | edge | `beta` | `hobby`, `pro`, `scale` | Serve streaming HTTP responses, SSE, and gRPC APIs. | `pkg/gateway/handler_test.go::TestRawStreamReverseProxy_RemoteWakeNode` |
| [Jobs and workflows](../docs/faas_openapi_spec) | async | `preview` | `hobby`, `pro`, `scale` | Run asynchronous jobs and durable workflows alongside an API. | `cmd/e2e/workflows_e2e_test.go::TestE2E_Workflows_Lifecycle` |

## Promotion rule

A capability may move from `internal` to `preview`, `beta`, or `ga` only when its acceptance evidence, documentation, plan entitlement, operational owner, and rollback/recovery procedure are updated in the same change. Numeric quotas remain defined in [`pkg/api/limits.go`](../pkg/api/limits.go).
