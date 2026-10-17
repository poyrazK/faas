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
    payload = {
        "request_id": str(request_id),
        "repository": "owner/repo",
        "title": "Fix",
        "recovery_url": "https://github.com/owner/repo/issues/42",
    }
    body = CreateNativeIssueTicketRequest.from_dict(payload)
    kwargs = create_native_issue_ticket._get_kwargs("payments", issue, body=body)
    assert kwargs["method"] == "post"
    assert kwargs["url"] == f"/v1/apps/payments/issues/{issue}/native-tickets"
    assert kwargs["json"] == payload
    result = NativeIssueTicketResult.from_dict(
        {"request_id": str(request_id), "state": "linked", "ticket_url": payload["recovery_url"], "link_id": str(link)}
    )
    assert result.request_id == request_id
    assert result.link_id == link
    assert result.state == "linked"


def test_auto_sync_routes_keep_revision_and_aligned_native_snapshot():
    from faas_sdk.api.apps import get_issue_tracker_auto_sync, update_issue_tracker_auto_sync
    from faas_sdk.models.issue_tracker_auto_sync import IssueTrackerAutoSync
    from faas_sdk.models.update_issue_tracker_auto_sync_request import UpdateIssueTrackerAutoSyncRequest

    issue, link = uuid4(), uuid4()
    status = {
        "issue_id": str(issue),
        "link_id": str(link),
        "installation_id": 42,
        "repository": "owner/repo",
        "repository_id": 100,
        "status": {"provider": "github", "url": "https://github.com/owner/repo/issues/42", "id": "42", "state": "open"},
    }
    payload = {"enabled": True, "expected_revision": 7, "expected_status": status, "expected_issue_state": "open"}
    body = UpdateIssueTrackerAutoSyncRequest.from_dict(payload)
    route = f"/v1/apps/payments/issues/{issue}/tracker-links/{link}/auto-sync"
    assert get_issue_tracker_auto_sync._get_kwargs("payments", issue, link)["url"] == route
    update = update_issue_tracker_auto_sync._get_kwargs("payments", issue, link, body=body)
    assert update["method"] == "put"
    assert update["url"] == route
    assert update["json"] == payload
    policy = IssueTrackerAutoSync.from_dict(
        {
            "link_id": str(link),
            "enabled": True,
            "revision": 8,
            "source_issue_id": str(issue),
            "outcome": "conflict",
            "reason": "provider_changed",
        }
    )
    assert policy.revision == 8
    assert policy.outcome == "conflict"
    assert policy.reason == "provider_changed"


def test_linear_connection_routes_preserve_revision_and_omit_key_on_disconnect():
    from faas_sdk.api.apps import get_issue_tracker_linear_connection, update_issue_tracker_linear_connection
    from faas_sdk.models.issue_tracker_linear_connection import IssueTrackerLinearConnection
    from faas_sdk.models.update_issue_tracker_linear_connection_request import UpdateIssueTrackerLinearConnectionRequest

    team = uuid4()
    payload = {"connected": True, "expected_revision": 7, "team_id": str(team), "api_key": "secret"}
    body = UpdateIssueTrackerLinearConnectionRequest.from_dict(payload)
    route = "/v1/apps/payments/issue-tracker-linear-connection"
    assert get_issue_tracker_linear_connection._get_kwargs("payments")["url"] == route
    request = update_issue_tracker_linear_connection._get_kwargs("payments", body=body)
    assert request["method"] == "put"
    assert request["url"] == route
    assert request["json"] == payload
    disconnected = UpdateIssueTrackerLinearConnectionRequest.from_dict({"connected": False, "expected_revision": 8})
    assert disconnected.to_dict() == {"connected": False, "expected_revision": 8}
    metadata = IssueTrackerLinearConnection.from_dict({"connected": False, "revision": 9})
    assert metadata.to_dict() == {"connected": False, "revision": 9}


def test_native_linear_creation_preserves_uuid_recovery_without_url():
    from faas_sdk.api.apps import create_native_linear_issue_ticket
    from faas_sdk.models.create_native_linear_issue_ticket_request import CreateNativeLinearIssueTicketRequest

    issue, request_id, team = uuid4(), uuid4(), uuid4()
    payload = {"request_id": str(request_id), "team_id": str(team), "title": "Fix", "recover": True}
    body = CreateNativeLinearIssueTicketRequest.from_dict(payload)
    request = create_native_linear_issue_ticket._get_kwargs("payments", issue, body=body)
    assert request["method"] == "post"
    assert request["url"] == f"/v1/apps/payments/issues/{issue}/native-linear-tickets"
    assert request["json"] == payload


def test_native_linear_status_preserves_preview_scope_and_workflow_selection():
    from faas_sdk.api.apps import get_native_linear_issue_tracker_status, update_native_linear_issue_tracker_status
    from faas_sdk.models.native_linear_issue_tracker_status import NativeLinearIssueTrackerStatus
    from faas_sdk.models.update_native_linear_issue_tracker_status_request import (
        UpdateNativeLinearIssueTrackerStatusRequest,
    )

    issue, link, ticket, workspace, viewer, workflow = [uuid4() for _ in range(6)]
    status = {
        "provider": "linear",
        "url": "https://linear.app/acme/issue/PAY-42",
        "id": str(ticket),
        "state": "resolved",
        "team_id": str(link),
        "state_id": str(workflow),
    }
    payload = {
        "issue_id": str(issue),
        "link_id": str(link),
        "connection_revision": 7,
        "group_fingerprint": "a" * 64,
        "workspace_id": str(workspace),
        "team_id": str(link),
        "viewer_id": str(viewer),
        "status": status,
    }
    preview = NativeLinearIssueTrackerStatus.from_dict(payload)
    assert preview.to_dict() == payload
    route = f"/v1/apps/payments/issues/{issue}/tracker-links/{link}/native-linear-status"
    assert get_native_linear_issue_tracker_status._get_kwargs("payments", issue, link)["url"] == route
    request = {
        "desired_state": "open",
        "expected": status,
        "expected_connection_revision": 7,
        "expected_group_fingerprint": "a" * 64,
        "linear_state_id": str(workflow),
    }
    body = UpdateNativeLinearIssueTrackerStatusRequest.from_dict(request)
    update = update_native_linear_issue_tracker_status._get_kwargs("payments", issue, link, body=body)
    assert update["method"] == "post"
    assert update["url"] == route
    assert update["json"] == request


def test_linear_auto_sync_preserves_mapping_and_native_scope():
    from faas_sdk.api.apps import update_issue_tracker_auto_sync
    from faas_sdk.models.issue_tracker_auto_sync import IssueTrackerAutoSync
    from faas_sdk.models.update_issue_tracker_auto_sync_request import UpdateIssueTrackerAutoSyncRequest

    issue, link, opened, resolved = uuid4(), uuid4(), uuid4(), uuid4()
    payload = {
        "enabled": True,
        "expected_revision": 3,
        "expected_issue_state": "open",
        "linear_open_state_id": str(opened),
        "linear_resolved_state_id": str(resolved),
        "expected_linear_status": {
            "issue_id": str(issue),
            "link_id": str(link),
            "connection_revision": 7,
            "group_fingerprint": "a" * 64,
            "workspace_id": str(issue),
            "team_id": str(issue),
            "viewer_id": str(issue),
            "status": {
                "provider": "linear",
                "url": "https://linear.app/acme/issue/PAY-42",
                "id": str(issue),
                "state": "open",
            },
        },
    }
    body = UpdateIssueTrackerAutoSyncRequest.from_dict(payload)
    assert update_issue_tracker_auto_sync._get_kwargs("payments", issue, link, body=body)["json"] == payload
    policy = IssueTrackerAutoSync.from_dict(
        {
            "link_id": str(link),
            "source_issue_id": str(issue),
            "enabled": True,
            "revision": 4,
            "outcome": "matched",
            "linear_open_state_id": str(opened),
            "linear_resolved_state_id": str(resolved),
        }
    )
    assert policy.linear_open_state_id == opened
    assert policy.linear_resolved_state_id == resolved


def test_linear_callback_secret_is_write_only_and_metadata_is_public():
    from faas_sdk.api.apps import update_issue_tracker_linear_connection
    from faas_sdk.models.issue_tracker_linear_connection import IssueTrackerLinearConnection
    from faas_sdk.models.update_issue_tracker_linear_connection_request import UpdateIssueTrackerLinearConnectionRequest

    secret = "linear_signing_secret_1234567890abcdef"
    payload = {
        "connected": True,
        "expected_revision": 2,
        "team_id": str(uuid4()),
        "api_key": "key",
        "webhook_secret": secret,
    }
    body = UpdateIssueTrackerLinearConnectionRequest.from_dict(payload)
    assert update_issue_tracker_linear_connection._get_kwargs("payments", body=body)["json"] == payload
    response = IssueTrackerLinearConnection.from_dict(
        {"connected": True, "revision": 3, "webhook_enabled": True, "callback_path": "/hooks/issue-trackers/linear/app"}
    )
    assert response.webhook_enabled is True
    assert response.callback_path == "/hooks/issue-trackers/linear/app"
    assert secret not in str(response.to_dict())


def test_linear_oauth_metadata_is_typed_and_contains_no_provider_credentials():
    from faas_sdk.models.issue_tracker_linear_connection import IssueTrackerLinearConnection

    metadata = IssueTrackerLinearConnection.from_dict(
        {
            "connected": True,
            "revision": 4,
            "auth_method": "oauth",
            "oauth_status": "reconnect_required",
            "access_token_expires_at": "2026-10-14T12:00:00Z",
        }
    )
    assert metadata.auth_method == "oauth"
    assert metadata.oauth_status == "reconnect_required"
    assert metadata.access_token_expires_at.isoformat() == "2026-10-14T12:00:00+00:00"
    assert "access_token" not in metadata.to_dict()
    assert "refresh_token" not in metadata.to_dict()


def test_directional_policy_preserves_opt_in_and_observed_hold():
    from faas_sdk.models.issue_tracker_auto_sync import IssueTrackerAutoSync
    from faas_sdk.models.update_issue_tracker_auto_sync_request import UpdateIssueTrackerAutoSyncRequest

    request = UpdateIssueTrackerAutoSyncRequest.from_dict(
        {"enabled": True, "expected_revision": 3, "direction": "inbound"}
    )
    assert request.to_dict()["direction"] == "inbound"
    response = IssueTrackerAutoSync.from_dict(
        {
            "link_id": "55555555-5555-4555-8555-555555555555",
            "source_issue_id": "33333333-3333-4333-8333-333333333333",
            "enabled": True,
            "revision": 4,
            "outcome": "blocked",
            "direction": "inbound",
            "observed_state": "resolved",
            "reason": "fixing_deployment_required",
        }
    )
    assert response.direction == "inbound"
    assert response.observed_state == "resolved"
    assert response.to_dict()["reason"] == "fixing_deployment_required"
