from uuid import UUID

from faas_sdk.api.apps import get_binding_verification_run_diagnostics
from faas_sdk.api.projects import get_project_binding_verification_run_diagnostics
from faas_sdk.models.binding_verification_run_diagnostics import BindingVerificationRunDiagnostics
from faas_sdk.models.project_binding_verification_run_diagnostics import ProjectBindingVerificationRunDiagnostics


def test_diagnostic_read_routes_and_public_models():
    identity = "11111111-1111-4111-8111-111111111111"
    member = {
        "budget": {"run_id": identity, "max_concurrency": 1, "active_resources": 1, "waiting_steps": 2, "full": True},
        "run_id": identity,
        "app": "api",
        "deployment_id": "22222222-2222-4222-8222-222222222222",
        "scope": "production",
        "status": "pending",
        "checked_at": "2026-10-14T00:00:00Z",
        "expires_at": "2026-10-14T00:01:00Z",
        "complete": True,
        "check_passed": False,
        "blockers": [
            {
                "code": "credential_generation_changed",
                "message": "Generation changed.",
                "recovery": "review_new_run",
                "type": "object_storage",
                "binding": "ASSETS",
            }
        ],
        "changes": [{"field": "credential_generation", "reviewed": "1", "current": "2"}],
        "recovery": [{"action": "review_new_run", "message": "Review a new run."}],
    }
    app = BindingVerificationRunDiagnostics.from_dict(member)
    assert app.budget.full is True
    assert app.budget.max_concurrency == 1
    assert app.check_passed is False
    assert app.changes[0].current == "2"
    graph = ProjectBindingVerificationRunDiagnostics.from_dict(
        {
            "run_id": identity,
            "project": "project",
            "environment": "production",
            "status": "pending",
            "checked_at": member["checked_at"],
            "expires_at": member["expires_at"],
            "complete": True,
            "budget": member["budget"],
            "members": {"api": member},
            "blockers": member["blockers"],
            "changes": member["changes"],
            "recovery": member["recovery"],
        }
    )
    assert graph.budget.run_id == UUID(identity)
    assert graph.members["api"].run_id == UUID(identity)
    assert graph.recovery[0].action == "review_new_run"
    for wire, url in [
        (
            get_binding_verification_run_diagnostics._get_kwargs("api", UUID(identity)),
            f"/v1/apps/api/bindings/verification-runs/{identity}/diagnostics",
        ),
        (
            get_project_binding_verification_run_diagnostics._get_kwargs("project", "production", UUID(identity)),
            f"/v1/projects/project/environments/production/release-sets/verification-runs/{identity}/diagnostics",
        ),
    ]:
        assert wire["method"] == "get"
        assert wire["url"] == url
