from uuid import UUID

import httpx

from faas_sdk.api.managed_postgres import (
    get_app_data_api_connection_budgets,
    get_managed_postgres_connection_budget,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.connection_budget_capacity import ConnectionBudgetCapacity
from faas_sdk.models.data_api_connection_budgets import DataAPIConnectionBudgets
from faas_sdk.models.managed_postgres_connection_budget import ManagedPostgresConnectionBudget


def test_verified_zero_capacity_preserves_source_and_freshness() -> None:
    value = {
        "state": "verified",
        "usable_connections": 0,
        "source": "provider_api",
        "checked_at": "2026-10-17T09:00:00+00:00",
        "expires_at": "2026-10-17T09:00:30+00:00",
    }
    capacity = ConnectionBudgetCapacity.from_dict(value)
    assert capacity.usable_connections == 0
    assert capacity.to_dict() == value


def test_runtime_coverage_preserves_reported_demand_and_missing_consumers() -> None:
    from faas_sdk.models.connection_budget_demand import ConnectionBudgetDemand

    value = {
        "data_api_resident_estimate": 4,
        "data_api_wake_estimate": 6,
        "auth_wake_estimate": 0,
        "control_headroom": 1,
        "migration_headroom": 0,
        "modeled_peak_connections": 7,
        "runtime_observations": {
            "state": "partial",
            "source": "workload_report",
            "observed_instances": 1,
            "unobserved_instances": 1,
            "parked_instances": 1,
            "configured_connections": 2,
            "checked_at": "2026-10-17T09:00:00+00:00",
            "expires_at": "2026-10-17T09:00:30+00:00",
        },
    }
    assert ConnectionBudgetDemand.from_dict(value).to_dict() == value


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
