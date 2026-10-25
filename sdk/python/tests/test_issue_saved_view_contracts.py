from uuid import uuid4

from faas_sdk.preview.api.apps import get_issue_saved_view, put_issue_saved_view
from faas_sdk.preview.models.issue_ownership_rule import IssueOwnershipRule
from faas_sdk.preview.models.issue_saved_view import IssueSavedView
from faas_sdk.preview.models.put_issue_saved_view_request import PutIssueSavedViewRequest


def test_saved_view_preserves_relative_owner_and_stable_key():
    payload = {
        "name": "Mine",
        "filters": {"state": "open", "assignee": "me", "sort": "impact", "tag": "component=payments"},
    }
    body = PutIssueSavedViewRequest.from_dict(payload)
    request = put_issue_saved_view._get_kwargs("payments", "mine", body=body)
    assert request["url"] == "/v1/apps/payments/issue-saved-views/mine"
    assert request["json"]["filters"]["assignee"] == "me"
    view = IssueSavedView.from_dict(
        {**payload, "key": "mine", "created_at": "2026-10-13T12:00:00Z", "updated_at": "2026-10-13T12:00:00Z"}
    )
    assert view.filters.assignee == "me" and view.key == "mine"
    assert get_issue_saved_view._get_kwargs("payments", "mine")["method"] == "get"


def test_source_and_tag_ownership_matchers_round_trip():
    payload = {
        "source_path_prefix": "/app/src/payments",
        "tag": "component=payments",
        "assignee_account_id": str(uuid4()),
    }
    assert IssueOwnershipRule.from_dict(payload).to_dict() == payload
