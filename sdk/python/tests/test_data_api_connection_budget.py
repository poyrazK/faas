from uuid import UUID

import httpx

from faas_sdk.api.managed_postgres import (
    get_app_data_api_connection_budgets,
    get_managed_postgres_connection_budget,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.data_api_connection_budgets import DataAPIConnectionBudgets
from faas_sdk.models.managed_postgres_connection_budget import ManagedPostgresConnectionBudget


def test_budget_clients_preserve_unknown_capacity_without_inventing_zero() -> None:
    database = UUID("11111111-1111-4111-8111-111111111111")
    budget = {
        "version": 1,
        "database_id": str(database),
        "database_name": "primary",
        "state": "unknown",
        "checked_at": "2026-10-16T00:00:00+00:00",
        "expires_at": "2026-10-16T00:00:30+00:00",
        "enforced": False,
        "model": "canonical_runtime_pools",
        "inventory": dict.fromkeys(
            "bindings data_api_consumers auth_consumers migration_bindings other_bindings "
            "resident_instances parked_instances auxiliary_instances active_tasks "
            "rotation_pending overlap_consumers stale_node_instances".split(),
            0,
        ),
        "demand": dict.fromkeys(
            "data_api_resident_estimate data_api_wake_estimate auth_wake_estimate "
            "control_headroom migration_headroom modeled_peak_connections".split(),
            0,
        ),
        "capacity": {"state": "unknown"},
        "reasons": ["capacity_unverified"],
    }
    report = {"version": 1, "app": "notes", "state": "unknown", "truncated": False, "databases": [budget]}
    requests = []

    def respond(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        assert request.method == "GET"
        assert request.headers["authorization"] == "Bearer fixture"
        if request.url.path == f"/v1/postgres/databases/{database}/connection-budget":
            return httpx.Response(200, json=budget)
        assert request.url.path == "/v1/apps/notes/data-api/connection-budget"
        return httpx.Response(200, json=report)

    client = AuthenticatedClient(base_url="https://management.example", token="fixture")
    client.set_httpx_client(
        httpx.Client(
            transport=httpx.MockTransport(respond),
            base_url="https://management.example",
            headers={"authorization": "Bearer fixture"},
        )
    )
    one = get_managed_postgres_connection_budget.sync(database, client=client)
    many = get_app_data_api_connection_budgets.sync("notes", client=client)
    assert isinstance(one, ManagedPostgresConnectionBudget)
    assert isinstance(many, DataAPIConnectionBudgets)
    assert one.to_dict() == budget
    assert many.to_dict() == report
    assert "usable_connections" not in one.capacity.to_dict()
    assert len(requests) == 2
