from uuid import uuid4

from faas_sdk.api.apps import delete_issue_tracker_link, list_issue_tracker_links, put_issue_tracker_link
from faas_sdk.models.issue_tracker_link import IssueTrackerLink
from faas_sdk.models.list_issue_tracker_links_response import ListIssueTrackerLinksResponse
from faas_sdk.models.put_issue_tracker_link_request import PutIssueTrackerLinkRequest


def test_tracker_link_management_and_provenance_contracts():
    issue, link = uuid4(), uuid4()
    body = PutIssueTrackerLinkRequest.from_dict({"url": "https://linear.app/acme/issue/PAY-1", "title": "Fix checkout"})
    request = put_issue_tracker_link._get_kwargs("payments", issue, body=body)
    base = f"/v1/apps/payments/issues/{issue}/tracker-links"
    assert request["url"] == base
    assert request["method"] == "post"
    assert request["json"] == body.to_dict()
    page_request = list_issue_tracker_links._get_kwargs("payments", issue, cursor="next+/=")
    assert page_request["url"] == base
    assert page_request["params"]["cursor"] == "next+/="
    deletion = delete_issue_tracker_link._get_kwargs("payments", issue, link)
    assert deletion["method"] == "delete"
    assert deletion["url"] == f"{base}/{link}"
    payload = {
        "id": str(link),
        "original_issue_id": str(issue),
        "provider": "linear",
        "external_key": "acme/PAY-1",
        "url": body.url,
        "created_at": "2026-10-14T12:00:00Z",
        "updated_at": "2026-10-14T12:00:00Z",
        "created_by_account_id": str(uuid4()),
    }
    stored = IssueTrackerLink.from_dict(payload)
    assert stored.original_issue_id == issue
    assert stored.external_key == "acme/PAY-1"
    page = ListIssueTrackerLinksResponse.from_dict({"items": [payload], "next_cursor": "tail"})
    assert page.items[0].id == link
    assert page.next_cursor == "tail"
