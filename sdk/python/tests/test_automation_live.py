from faas_sdk.preview.api.workflows.start_automation_live_verification import _get_kwargs
from faas_sdk.preview.models.automation_live_expectation import AutomationLiveExpectation
from faas_sdk.preview.models.automation_live_verification import AutomationLiveVerification
from faas_sdk.preview.models.automation_test_case import AutomationTestCase
from faas_sdk.preview.models.start_automation_live_verification_request import StartAutomationLiveVerificationRequest


def test_live_expectations_preserve_null_zero_and_large_numbers():
    for value in [
        {"step": "main", "output": None, "attempt_count": 0},
        {"step": "main", "output": {"id": 9007199254740993}, "state": "succeeded"},
        {"step": "main", "state": "succeeded"},
    ]:
        assert AutomationLiveExpectation.from_dict(value).to_dict() == value
    case = {
        "name": "synthetic",
        "simulation": {"input": {"id": 9007199254740993}},
        "expectations": [{"step": "main", "state": "mocked"}],
        "live": {"expectations": [{"step": "main", "state": "succeeded", "output": None}]},
    }
    assert AutomationTestCase.from_dict(case).to_dict() == case


def test_live_start_requires_explicit_key_and_target_fences():
    body = {
        "expected_version": 7,
        "expected_published_version": 6,
        "expected_deployment_id": "compact-deployment-id",
        "case_name": "synthetic",
        "confirm_real_actions": True,
    }
    request = StartAutomationLiveVerificationRequest.from_dict(body)
    assert request.to_dict() == body
    kwargs = _get_kwargs("billing", "report", body=request, idempotency_key="stable-live-key")
    assert kwargs["headers"]["Idempotency-Key"] == "stable-live-key"
    assert kwargs["url"] == "/v1/apps/billing/automations/report/live-verifications"
    assert kwargs["json"] == body
    report = {
        "run_id": "018e7310-2222-4333-8444-555555555555",
        "case_name": "synthetic",
        "deployment_id": "compact-deployment-id",
        "inputs_hash": "a" * 64,
        "run_status": "succeeded",
        "version": 7,
        "published_version": 6,
        "created_at": "2026-10-15T10:00:00+00:00",
        "outdated": False,
        "report_current": True,
        "report": {
            "checked_at": "2026-10-15T10:00:00+00:00",
            "passed": False,
            "run_status": "succeeded",
            "resume_count": 0,
            "run_updated_at": "2026-10-15T10:00:00+00:00",
            "issue": "assertion_failed",
            "steps": [{"step": "main", "passed": False, "state": "succeeded", "mismatches": ["output"]}],
        },
    }
    assert AutomationLiveVerification.from_dict(report).to_dict() == report


def test_shared_suite_metadata_routes_and_missing_run_identity():
    from uuid import UUID

    from faas_sdk.preview.api.workflows.get_automation_live_suite import _get_kwargs as get_kwargs
    from faas_sdk.preview.api.workflows.list_automation_live_suites import _get_kwargs as list_kwargs
    from faas_sdk.preview.api.workflows.save_automation_live_suite import _get_kwargs as save_kwargs
    from faas_sdk.preview.models.automation_live_suite import AutomationLiveSuite
    from faas_sdk.preview.models.save_automation_live_suite_request import SaveAutomationLiveSuiteRequest

    suite_id = UUID("00000000-0000-4000-8000-000000000010")
    run_id = "00000000-0000-4000-8000-000000000011"
    deployment_id = "00000000-0000-4000-8000-000000000002"
    body = {
        "expected_version": 7,
        "expected_published_version": 4,
        "expected_deployment_id": deployment_id,
        "cases": [{"name": "first", "run_id": run_id}, {"name": "second"}],
    }
    request = SaveAutomationLiveSuiteRequest.from_dict(body)
    assert request.to_dict() == body
    kwargs = save_kwargs("billing", "case/report", suite_id, body=request)
    root = "/v1/apps/billing/automations/case%2Freport/live-suites"
    assert kwargs["method"] == "put" and kwargs["json"] == body
    assert kwargs["url"] == root + "/" + str(suite_id)
    assert get_kwargs("billing", "case/report", suite_id)["url"] == kwargs["url"]
    assert list_kwargs("billing", "case/report")["url"] == root
    view = {
        "id": str(suite_id),
        "name": "case/report",
        "version": 7,
        "published_version": 4,
        "deployment_id": deployment_id,
        "created_at": "2026-10-15T10:00:00+00:00",
        "updated_at": "2026-10-15T10:00:00+00:00",
        "case_count": 2,
        "linked_runs": 1,
        "outcome": "unavailable",
        "managed": True,
        "outdated": False,
        "observed_at": "2026-10-15T10:00:00+00:00",
        "summary": {
            "selected": 2,
            "passed": 0,
            "failed": 0,
            "pending": 0,
            "not_started": 1,
            "unavailable": 1,
            "outdated": 0,
        },
        "cases": [
            {"name": "first", "run_id": run_id, "outcome": "unavailable"},
            {"name": "second", "outcome": "not_started"},
        ],
    }
    assert AutomationLiveSuite.from_dict(view).to_dict() == view


def test_suite_comparison_reads_explicit_pair_and_preserves_unknown_evidence():
    from uuid import UUID

    from faas_sdk.preview.api.workflows.compare_automation_live_suites import _get_kwargs
    from faas_sdk.preview.models.automation_live_suite_comparison import AutomationLiveSuiteComparison

    candidate = UUID("00000000-0000-4000-8000-000000000001")
    baseline = UUID("00000000-0000-4000-8000-000000000002")
    kwargs = _get_kwargs("billing", "case/report", candidate, baseline)
    assert kwargs["method"] == "get" and "json" not in kwargs
    assert kwargs["url"] == f"/v1/apps/billing/automations/case%2Freport/live-suites/{candidate}/compare/{baseline}"
    summary = {
        "id": str(baseline),
        "name": "report",
        "version": 7,
        "published_version": 4,
        "deployment_id": "00000000-0000-4000-8000-000000000003",
        "created_at": "2026-10-15T10:00:00+00:00",
        "updated_at": "2026-10-15T10:00:00+00:00",
        "case_count": 1,
        "linked_runs": 0,
    }
    view = {
        "baseline": summary,
        "candidate": {**summary, "id": str(candidate)},
        "baseline_outdated": True,
        "candidate_outdated": False,
        "observed_at": "2026-10-15T10:00:00+00:00",
        "summary": {
            "total": 1,
            "regressions": 0,
            "recoveries": 0,
            "unchanged": 0,
            "added": 0,
            "removed": 0,
            "incompatible": 0,
            "unknown": 1,
        },
        "cases": [
            {
                "name": "same-case",
                "change": "unknown",
                "baseline": {"result": "not_started"},
                "candidate": {"result": "unavailable", "created_at": "2026-10-15T09:00:00+00:00"},
            }
        ],
    }
    assert AutomationLiveSuiteComparison.from_dict(view).to_dict() == view


def test_managed_suites_preserve_target_and_explicit_continuation():
    from uuid import UUID

    from faas_sdk.preview.api.workflows.advance_automation_live_suite import _get_kwargs as advance_kwargs
    from faas_sdk.preview.api.workflows.start_automation_live_suite import _get_kwargs as start_kwargs
    from faas_sdk.preview.models.advance_automation_live_suite_request import AdvanceAutomationLiveSuiteRequest
    from faas_sdk.preview.models.automation_live_suite_summary import AutomationLiveSuiteSummary
    from faas_sdk.preview.models.start_automation_live_suite_request import StartAutomationLiveSuiteRequest

    suite_id = UUID("0199e820-0000-7000-8000-000000000010")
    body = {
        "expected_version": 7,
        "expected_published_version": 4,
        "expected_deployment_id": "00000000-0000-4000-8000-000000000002",
        "cases": ["first", "second"],
        "confirm_real_actions": True,
    }
    request = StartAutomationLiveSuiteRequest.from_dict(body)
    assert request.to_dict() == body
    root = "/v1/apps/billing/automations/case%2Freport/live-suites/" + str(suite_id)
    start = start_kwargs("billing", "case/report", suite_id, body=request)
    assert start["method"] == "post" and start["url"] == root + "/start"
    assert start["json"] == body
    advance = advance_kwargs(
        "billing", "case/report", suite_id, body=AdvanceAutomationLiveSuiteRequest(confirm_real_actions=True)
    )
    assert advance["method"] == "post" and advance["url"] == root + "/advance"
    assert advance["json"] == {"confirm_real_actions": True}
    summary = {
        "id": str(suite_id),
        "name": "report",
        "version": 7,
        "published_version": 4,
        "deployment_id": body["expected_deployment_id"],
        "managed": True,
        "created_at": "2026-10-15T10:00:00+00:00",
        "updated_at": "2026-10-15T10:00:00+00:00",
        "case_count": 2,
        "linked_runs": 1,
    }
    assert AutomationLiveSuiteSummary.from_dict(summary).to_dict() == summary


def test_named_baselines_preserve_explicit_promotion_and_audit_receipts():
    from uuid import UUID

    from faas_sdk.preview.api.workflows.get_automation_live_baseline import _get_kwargs as get_head
    from faas_sdk.preview.api.workflows.get_automation_live_baseline_history import _get_kwargs as get_history
    from faas_sdk.preview.api.workflows.promote_automation_live_baseline import _get_kwargs as promote
    from faas_sdk.preview.models.automation_live_baseline import AutomationLiveBaseline
    from faas_sdk.preview.models.automation_live_baseline_history import AutomationLiveBaselineHistory
    from faas_sdk.preview.models.promote_automation_live_baseline_request import PromoteAutomationLiveBaselineRequest

    promotion_id = UUID("00000000-0000-4000-8000-000000000020")
    suite_id = "00000000-0000-4000-8000-000000000010"
    deployment_id = "00000000-0000-4000-8000-000000000002"
    policy = {"version": 2, "required_cases": ["happy"], "max_candidate_age_seconds": 3600}
    request = {
        "suite_id": suite_id,
        "expected_revision": 0,
        "expected_version": 7,
        "expected_published_version": 4,
        "expected_deployment_id": deployment_id,
        "policy": policy,
    }
    body = PromoteAutomationLiveBaselineRequest.from_dict(request)
    kwargs = promote("billing", "case/report", "production", promotion_id, body=body)
    root = "/v1/apps/billing/automations/case%2Freport/live-baselines/production"
    assert kwargs["method"] == "put" and kwargs["url"] == f"{root}/promotions/{promotion_id}"
    assert kwargs["json"] == request
    for reader, suffix in [(get_head, ""), (get_history, "/history")]:
        read = reader("billing", "case/report", "production")
        assert read["method"] == "get" and read["url"] == root + suffix and "json" not in read
    receipt = {
        "promotion_id": str(promotion_id),
        "app_id": deployment_id,
        "name": "case/report",
        "baseline_name": "production",
        "revision": 1,
        "actor_account_id": deployment_id,
        "promoted_at": "2026-10-16T10:00:00+00:00",
        "suite": {
            "id": suite_id,
            "name": "case/report",
            "version": 7,
            "published_version": 4,
            "deployment_id": deployment_id,
            "created_at": "2026-10-16T09:59:00+00:00",
            "updated_at": "2026-10-16T09:59:00+00:00",
            "case_count": 1,
            "linked_runs": 1,
        },
        "policy": policy,
        "policy_hash": "a" * 64,
        "cases_passed": 1,
        "oldest_run_created_at": "2026-10-16T09:59:00+00:00",
        "evidence_age_seconds": 60.0,
    }
    assert AutomationLiveBaseline.from_dict(receipt).to_dict() == receipt
    history = {"promotions": [receipt]}
    assert AutomationLiveBaselineHistory.from_dict(history).to_dict() == history

    from faas_sdk.preview.api.workflows.list_automation_live_baselines import _get_kwargs as list_baselines
    from faas_sdk.preview.models.list_automation_live_baselines_response import ListAutomationLiveBaselinesResponse

    listing = list_baselines("billing", "case/report")
    assert listing["method"] == "get"
    assert listing["url"] == "/v1/apps/billing/automations/case%2Freport/live-baselines"
    assert "json" not in listing
    payload = {
        "observed_at": "2026-10-16T12:00:00+00:00",
        "baselines": [
            {
                "baseline": receipt,
                "evidence_status": "usable",
                "renewal_status": "inconclusive",
                "outdated": True,
                "reasons": ["candidate_outdated"],
                "cases": [{"name": "happy", "run_id": suite_id, "status": "passed"}],
                "age_seconds": 3601.0,
                "max_age_seconds": 3600,
            }
        ],
    }
    assert ListAutomationLiveBaselinesResponse.from_dict(payload).to_dict() == payload


def test_baseline_retirement_request_and_history_roundtrip():
    from faas_sdk.preview.api.workflows.retire_automation_live_baseline import _get_kwargs
    from faas_sdk.preview.models.automation_live_baseline_history import AutomationLiveBaselineHistory
    from faas_sdk.preview.models.automation_live_baseline_retirement import AutomationLiveBaselineRetirement
    from faas_sdk.preview.models.retire_automation_live_baseline_request import RetireAutomationLiveBaselineRequest

    identity = "00000000-0000-4000-8000-000000000014"
    request = RetireAutomationLiveBaselineRequest(expected_revision=3)
    kwargs = _get_kwargs("billing", "case/report", "production", identity, body=request)
    assert kwargs["method"] == "put"
    assert (
        kwargs["url"] == f"/v1/apps/billing/automations/case%2Freport/live-baselines/production/retirements/{identity}"
    )
    assert kwargs["json"] == {"expected_revision": 3}
    receipt = {
        "retirement_id": identity,
        "app_id": "00000000-0000-4000-8000-000000000001",
        "name": "case/report",
        "baseline_name": "production",
        "revision": 4,
        "previous_promotion_id": "00000000-0000-4000-8000-000000000013",
        "actor_account_id": "00000000-0000-4000-8000-000000000002",
        "retired_at": "2026-10-16T12:00:00+00:00",
    }
    assert AutomationLiveBaselineRetirement.from_dict(receipt).to_dict() == receipt
    history = {"promotions": [], "retirement": receipt}
    assert AutomationLiveBaselineHistory.from_dict(history).to_dict() == history
