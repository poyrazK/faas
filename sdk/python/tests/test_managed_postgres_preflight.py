from uuid import UUID

import httpx

from faas_sdk.preview.api.managed_postgres import (
    get_managed_postgres_cutover_preflight,
    request_managed_postgres_cutover_preflight,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.preview.models.managed_postgres_cutover_preflight import ManagedPostgresCutoverPreflight


def test_preflight_progress_preserves_disabled_gate_and_historical_findings() -> None:
    cutover_id = UUID("33333333-3333-4333-8333-333333333333")
    binding_id = UUID("44444444-4444-4444-8444-444444444444")
    progress = {
        "cutover_id": str(cutover_id),
        "state": "pending",
        "revision": 1,
        "activation_allowed": False,
        "stale": True,
        "binding_ids": [],
        "blockers": [],
        "verification_fresh": False,
        "source_matches": False,
        "target_matches": False,
        "bindings_match": False,
        "created_at": "2026-10-15T00:00:00Z",
        "updated_at": "2026-10-15T00:00:00Z",
    }
    requests = []

    def respond(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        assert request.headers["authorization"] == "Bearer fixture"
        assert request.url.path == f"/v1/postgres/cutovers/{cutover_id}/preflight"
        if request.method == "POST":
            assert request.headers["idempotency-key"] == "preflight-first"
            return httpx.Response(202, json=progress)
        assert request.method == "GET"
        return httpx.Response(
            200,
            json={
                **progress,
                "state": "blocked",
                "binding_ids": [str(binding_id)],
                "blockers": ["activation_disabled"],
                "verification_fresh": True,
                "source_sessions": {
                    "state": "closed",
                    "admission_closed": True,
                    "selected_databases_drained": False,
                    "selected_database_count": 2,
                    "sessions": 3,
                    "prepared_transactions": 1,
                    "unselected_databases": 1,
                    "blockers": [
                        "source_sessions_active",
                        "prepared_transactions_pending",
                        "database_coverage_incomplete",
                    ],
                    "observed_at": "2026-10-15T00:00:00Z",
                    "writer_isolation": {
                        "state": "unsupported",
                        "confirmed": False,
                        "inventory_available": True,
                        "catalog_matches": False,
                        "catalog_stable": False,
                        "background_writers_fenced": False,
                        "client_sessions": 3,
                        "background_workers": 1,
                        "replication_workers": 0,
                        "unknown_backends": 2,
                    },
                },
                "runtime_drain": {
                    "state": "blocked",
                    "inventory_count": 2,
                    "pending": 1,
                    "retired": 0,
                    "blocked": 1,
                    "blockers": ["owner_revision_changed"],
                    "members": [
                        {
                            "instance_id": "55555555-5555-4555-8555-555555555555",
                            "scope": "production",
                            "mode": "job",
                            "state": "pending",
                            "blocker_code": "native_retirement_unconfirmed",
                        },
                        {
                            "instance_id": "66666666-6666-4666-8666-666666666666",
                            "scope": "staging",
                            "mode": "worker",
                            "state": "blocked",
                            "blocker_code": "parked_instance_unproven",
                        },
                    ],
                },
            },
        )

    client = AuthenticatedClient(
        base_url="https://api.example.test", token="fixture", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    with client:
        pending = request_managed_postgres_cutover_preflight.sync(
            cutover_id, client=client, idempotency_key="preflight-first"
        )
        assert isinstance(pending, ManagedPostgresCutoverPreflight)
        assert pending.state == "pending" and pending.activation_allowed is False
        checked = get_managed_postgres_cutover_preflight.sync(cutover_id, client=client)
    assert isinstance(checked, ManagedPostgresCutoverPreflight)
    assert checked.state == "blocked" and checked.activation_allowed is False
    assert checked.stale and checked.verification_fresh  # Historical success is not current authority.
    assert checked.binding_ids == [binding_id]
    assert checked.blockers[0] == "activation_disabled"
    assert checked.runtime_drain.state == "blocked"
    assert checked.runtime_drain.pending == 1 and checked.runtime_drain.blocked == 1
    assert checked.runtime_drain.members[0].scope == "production"
    assert checked.runtime_drain.members[1].blocker_code == "parked_instance_unproven"
    assert checked.source_sessions.admission_closed and not checked.source_sessions.selected_databases_drained
    assert checked.source_sessions.sessions == 3 and checked.source_sessions.prepared_transactions == 1
    assert checked.source_sessions.unselected_databases == 1
    assert checked.source_sessions.writer_isolation.state == "unsupported"
    assert not checked.source_sessions.writer_isolation.confirmed
    assert checked.source_sessions.writer_isolation.inventory_available
    assert checked.source_sessions.writer_isolation.background_workers == 1
    assert checked.source_sessions.writer_isolation.unknown_backends == 2
    assert (
        ManagedPostgresCutoverPreflight.from_dict(checked.to_dict()).source_sessions.writer_isolation.unknown_backends
        == 2
    )
    assert ManagedPostgresCutoverPreflight.from_dict(checked.to_dict()).source_sessions.state == "closed"
    assert ManagedPostgresCutoverPreflight.from_dict(checked.to_dict()).runtime_drain.members[0].state == "pending"
    assert len(requests) == 2
