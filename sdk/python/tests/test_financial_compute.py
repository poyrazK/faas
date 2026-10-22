from faas_sdk.models.financial_budget_runtime import FinancialBudgetRuntime


def test_strict_compute_retains_exact_funding_and_unconfirmed_retirement():
    compute = {
        "initialized": True,
        "app_id": "6dc4f678-5766-4a06-a061-845c2b133fdd",
        "period_start": "2026-10-01T00:00:00Z",
        "period_end": "2026-11-01T00:00:00Z",
        "committed_millicents": 9007199254740000,
        "reserved_millicents": 991,
        "remaining_millicents": 0,
        "unconfirmed_leases": 1,
        "held": True,
        "observed_at": "2026-10-17T10:00:00Z",
    }
    runtime = FinancialBudgetRuntime.from_dict({"alerts": [], "compute": compute})
    assert runtime.compute.committed_millicents == 9007199254740000
    assert runtime.compute.reserved_millicents == 991
    assert runtime.compute.unconfirmed_leases == 1
    assert runtime.compute.held
    assert runtime.to_dict()["compute"]["remaining_millicents"] == 0


def test_account_compute_quantity_allowance_has_no_fake_app_identity():
    compute = {
        "initialized": True,
        "account_id": "6dc4f678-5766-4a06-a061-845c2b133fdd",
        "scope_kind": "account",
        "basis": "net_usage",
        "period_start": "2026-10-01T00:00:00Z",
        "period_end": "2026-11-01T00:00:00Z",
        "committed_millicents": 1000,
        "reserved_millicents": 25,
        "remaining_millicents": 975,
        "allowance_granted_mb_seconds": 9007199254740991,
        "allowance_committed_mb_seconds": 9007199254740000,
        "allowance_reserved_mb_seconds": 991,
        "allowance_remaining_mb_seconds": 0,
        "unconfirmed_leases": 2,
        "held": False,
        "observed_at": "2026-10-17T10:00:00Z",
    }
    runtime = FinancialBudgetRuntime.from_dict({"alerts": [], "compute": compute})
    assert runtime.compute.allowance_granted_mb_seconds == 9007199254740991
    assert runtime.compute.allowance_reserved_mb_seconds == 991
    serialized = runtime.to_dict()["compute"]
    assert "app_id" not in serialized
    assert serialized["basis"] == "net_usage"
    assert serialized["allowance_remaining_mb_seconds"] == 0


def test_job_compute_preserves_scope_gross_spending_and_hold():
    compute = {
        "initialized": True,
        "job_id": "6dc4f678-5766-4a06-a061-845c2b133fdd",
        "scope_kind": "job",
        "basis": "gross_usage",
        "period_start": "2026-10-01T00:00:00Z",
        "period_end": "2026-11-01T00:00:00Z",
        "committed_millicents": 9007199254740000,
        "reserved_millicents": 991,
        "remaining_millicents": 0,
        "unconfirmed_leases": 1,
        "held": True,
        "observed_at": "2026-10-17T10:00:00Z",
    }
    runtime = FinancialBudgetRuntime.from_dict({"alerts": [], "compute": compute})
    serialized = runtime.to_dict()["compute"]
    assert serialized["job_id"] == compute["job_id"]
    assert serialized["scope_kind"] == "job"
    assert serialized["basis"] == "gross_usage"
    assert serialized["committed_millicents"] + serialized["reserved_millicents"] == 9007199254740991
    assert serialized["held"]
    assert "app_id" not in serialized


def test_shared_account_retains_separate_exact_egress_credits():
    egress = {
        "initialized": True,
        "committed_bytes": 9007199254740000,
        "reserved_bytes": 991,
        "allowance_granted_bytes": 9007199254740991,
        "allowance_committed_bytes": 9007199254740000,
        "allowance_reserved_bytes": 991,
        "allowance_remaining_bytes": 0,
        "committed_millicents": 1000,
        "reserved_millicents": 25,
    }
    compute = {
        "initialized": True,
        "period_start": "2026-10-01T00:00:00Z",
        "period_end": "2026-11-01T00:00:00Z",
        "committed_millicents": 2000,
        "reserved_millicents": 50,
        "remaining_millicents": 0,
        "unconfirmed_leases": 1,
        "held": True,
        "observed_at": "2026-10-17T10:00:00Z",
        "egress": egress,
    }
    runtime = FinancialBudgetRuntime.from_dict({"alerts": [], "compute": compute})
    assert runtime.compute.egress.committed_bytes + runtime.compute.egress.reserved_bytes == 9007199254740991
    assert runtime.to_dict()["compute"]["egress"] == egress
    assert runtime.compute.committed_millicents == 2000
