from uuid import uuid4

from faas_sdk.api.apps import get_native_issue_tracker_status, update_native_issue_tracker_status
from faas_sdk.models.native_issue_tracker_status import NativeIssueTrackerStatus
from faas_sdk.models.update_native_issue_tracker_status_request import UpdateNativeIssueTrackerStatusRequest


def test_native_status_routes_preserve_expected_snapshot_without_provider_credentials():
    issue, link = uuid4(), uuid4()
    status = {"provider": "github", "url": "https://github.com/owner/repo/issues/42", "id": "42", "state": "resolved"}
    payload = {
        "issue_id": str(issue),
        "link_id": str(link),
        "installation_id": 42,
        "repository": "owner/repo",
        "repository_id": 100,
        "status": status,
    }
    observed = NativeIssueTrackerStatus.from_dict(payload)
    assert observed.issue_id == issue
    assert observed.link_id == link
    assert observed.repository_id == 100
    assert observed.status.to_dict() == status
    route = f"/v1/apps/payments/issues/{issue}/tracker-links/{link}/native-status"
    assert get_native_issue_tracker_status._get_kwargs("payments", issue, link)["url"] == route
    body = UpdateNativeIssueTrackerStatusRequest.from_dict({"desired_state": "open", "expected": status})
    update = update_native_issue_tracker_status._get_kwargs("payments", issue, link, body=body)
    assert update["method"] == "post"
    assert update["url"] == route
    assert update["json"] == {"desired_state": "open", "expected": status}


def test_native_creation_retains_request_identity_and_recovery_url():
    from faas_sdk.api.apps import create_native_issue_ticket
    from faas_sdk.models.create_native_issue_ticket_request import CreateNativeIssueTicketRequest
    from faas_sdk.models.native_issue_ticket_result import NativeIssueTicketResult

    issue, request_id, link = uuid4(), uuid4(), uuid4()
    payload = {"request_id": str(request_id), "repository": "owner/repo", "title": "Fix", "recovery_url": "https://github.com/owner/repo/issues/42"}
    body = CreateNativeIssueTicketRequest.from_dict(payload)
    kwargs = create_native_issue_ticket._get_kwargs("payments", issue, body=body)
    assert kwargs["method"] == "post"
    assert kwargs["url"] == f"/v1/apps/payments/issues/{issue}/native-tickets"
    assert kwargs["json"] == payload
    result = NativeIssueTicketResult.from_dict({"request_id": str(request_id), "state": "linked", "ticket_url": payload["recovery_url"], "link_id": str(link)})
    assert result.request_id == request_id
    assert result.link_id == link
    assert result.state == "linked"
