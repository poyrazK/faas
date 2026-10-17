import httpx

from faas_sdk.api.queues import lease_org_queue_messages, ack_org_queue_leases, get_org_queue_usage
from faas_sdk.api.projects import get_org_project_environment_queue_bindings
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.queue_lease_receipts_request import QueueLeaseReceiptsRequest


def test_company_queues_preserve_ownership_paths() -> None:
    paths: list[tuple[str, bytes]] = []

    def respond(request: httpx.Request) -> httpx.Response:
        paths.append((request.method, request.url.raw_path))
        assert request.headers["authorization"] == "Bearer company-token"
        if request.url.path.endswith("queue-usage"):
            return httpx.Response(
                200,
                json={
                    "organization_id": "00000000-0000-0000-0000-000000000001",
                    "plan": "pro",
                    "bindings": 1,
                    "pending": 0,
                    "processing": 0,
                    "dead_letter": 0,
                    "reserved_async": 0,
                    "max_async_invocations": 10000,
                    "max_triggers": 10,
                },
            )
        if request.url.path.endswith("queue-bindings"):
            return httpx.Response(
                200,
                json={
                    "environment": "stage",
                    "workload": "worker/id",
                    "revision": 1,
                    "workload_revision": 1,
                    "config_hash": "hash",
                    "activation_state": "unavailable",
                    "bindings": [],
                },
            )
        if request.url.path.endswith("leases:ack"):
            return httpx.Response(200, json={"succeeded": ["receipt"], "failed": []})
        return httpx.Response(200, json={"messages": []})

    client = AuthenticatedClient(
        base_url="https://api.example.test",
        token="company-token",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    with client:
        leased = lease_org_queue_messages.sync(organization="my company", slug="app/id", queue="jobs/id", client=client)
        assert leased is not None and leased.messages == []
        settled = ack_org_queue_leases.sync(
            organization="my company",
            slug="app/id",
            queue="jobs/id",
            client=client,
            body=QueueLeaseReceiptsRequest(receipts=["receipt"]),
        )
        assert settled is not None and settled.succeeded == ["receipt"]
        config = get_org_project_environment_queue_bindings.sync(
            organization="my company", slug="project/id", environment="stage", workload="worker/id", client=client
        )
        assert config is not None and config.bindings == []
        usage = get_org_queue_usage.sync(organization="my company", client=client)
        assert usage is not None and usage.bindings == 1 and usage.reserved_async == 0
    assert paths == [
        ("POST", b"/v1/orgs/my%20company/apps/app%2Fid/queues/jobs%2Fid/leases"),
        ("POST", b"/v1/orgs/my%20company/apps/app%2Fid/queues/jobs%2Fid/leases:ack"),
        ("GET", b"/v1/orgs/my%20company/projects/project%2Fid/environments/stage/workloads/worker%2Fid/queue-bindings"),
        ("GET", b"/v1/orgs/my%20company/queue-usage"),
    ]
