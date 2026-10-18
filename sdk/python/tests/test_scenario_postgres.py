from faas_sdk.models.create_scenario_postgres_request import CreateScenarioPostgresRequest
from faas_sdk.models.scenario_postgres_evidence import ScenarioPostgresEvidence


def test_postgres_fixture_sql_and_duplicate_effects_round_trip():
    spec = {
        "version": 1,
        "fixture_sha256": "a" * 64,
        "tables": [{"schema": "public", "name": "imports", "columns": ["item"]}],
        "max_lock_wait_ms": 50,
    }
    request = {
        "app_slug": "isolated-api",
        "spec": spec,
        "sql": "CREATE TABLE public.imports(item text);\nINSERT INTO public.imports VALUES ('é');\n",
    }
    assert CreateScenarioPostgresRequest.from_dict(request).to_dict() == request
    duplicate_rows = [{"item": "same"}, {"item": "same"}]
    evidence = {
        "version": 1,
        "run_id": "b" * 32,
        "app_id": "11111111-1111-4111-8111-111111111111",
        "resource_id": "22222222-2222-4222-8222-222222222222",
        "deployment_id": "33333333-3333-4333-8333-333333333333",
        "fixture_sha256": "a" * 64,
        "contract_sha256": "c" * 64,
        "initial_sha256": "d" * 64,
        "status": "complete",
        "provenance": "platform_postgres_collector",
        "spec": spec,
        "before": [{"table": "public.imports", "rows": []}],
        "after": [{"table": "public.imports", "rows": duplicate_rows}],
        "changes": [{"table": "public.imports", "inserted": duplicate_rows, "deleted": []}],
        "lock_samples": 3,
        "max_observed_lock_wait_ms": 100,
        "lock_budget_passed": False,
        "cleanup_confirmed": True,
    }
    assert ScenarioPostgresEvidence.from_dict(evidence).to_dict() == evidence
