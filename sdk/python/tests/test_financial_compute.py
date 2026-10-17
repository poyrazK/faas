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
