from uuid import uuid4

from faas_sdk.preview.api.apps import bulk_act_on_issues, list_issues, put_issue_team
from faas_sdk.preview.models.bulk_issue_action_request import BulkIssueActionRequest
from faas_sdk.preview.models.issue_ownership_rule import IssueOwnershipRule
from faas_sdk.preview.models.issue_saved_view_filters import IssueSavedViewFilters
from faas_sdk.preview.models.put_issue_team_request import PutIssueTeamRequest


def test_team_roster_inbox_and_assignment_contracts():
    team, member, issue = uuid4(), uuid4(), uuid4()
    body = PutIssueTeamRequest.from_dict({"name": "Payments", "member_account_ids": [str(member)]})
    request = put_issue_team._get_kwargs("payments", "payments", body=body)
    assert request["url"] == "/v1/apps/payments/issue-teams/payments"
    assert request["json"]["member_account_ids"] == [str(member)]
    assert list_issues._get_kwargs("payments", team=team)["params"]["team"] == str(team)
    payload = {"issue_ids": [str(issue)], "action": {"action": "assign", "assignee_team_id": str(team)}}
    bulk = BulkIssueActionRequest.from_dict(payload)
    assert bulk_act_on_issues._get_kwargs("payments", body=bulk)["json"] == payload
    rule = IssueOwnershipRule.from_dict({"source_kind": "exception", "assignee_team_id": str(team)})
    assert rule.to_dict() == {"source_kind": "exception", "assignee_team_id": str(team)}
    assert IssueSavedViewFilters.from_dict({"team": str(team)}).to_dict() == {"team": str(team)}


def test_existing_positional_owner_and_view_constructors_remain_compatible():
    account = uuid4()
    assert IssueOwnershipRule(account).assignee_account_id == account
    assert IssueSavedViewFilters("open").state == "open"
