from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_action_request_action import IssueActionRequestAction, check_issue_action_request_action
from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueActionRequest")


@_attrs_define
class IssueActionRequest:
    """Desired ownership, lifecycle, or reversible regrouping action within the app. Merge adopts the destination
    owner/state; split opens one linked original group. Merge/split responses identify the remaining canonical issue.

    """

    action: IssueActionRequestAction
    target_issue_id: UUID | Unset = UNSET
    """Required only for merge; destination in the same app and environment."""
    member_issue_id: UUID | Unset = UNSET
    """Required only for split; linked original identity to detach."""
    assignee_account_id: UUID | Unset = UNSET
    fixed_deployment_id: UUID | Unset = UNSET
    ignored_until: datetime.datetime | Unset = UNSET
    assignee_team_id: UUID | Unset = UNSET
    """Target team for this assignment action; clear the account assignee when selecting a team."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        target_issue_id: str | Unset = UNSET
        if not isinstance(self.target_issue_id, Unset):
            target_issue_id = str(self.target_issue_id)

        member_issue_id: str | Unset = UNSET
        if not isinstance(self.member_issue_id, Unset):
            member_issue_id = str(self.member_issue_id)

        assignee_account_id: str | Unset = UNSET
        if not isinstance(self.assignee_account_id, Unset):
            assignee_account_id = str(self.assignee_account_id)

        fixed_deployment_id: str | Unset = UNSET
        if not isinstance(self.fixed_deployment_id, Unset):
            fixed_deployment_id = str(self.fixed_deployment_id)

        ignored_until: str | Unset = UNSET
        if not isinstance(self.ignored_until, Unset):
            ignored_until = self.ignored_until.isoformat()

        assignee_team_id: str | Unset = UNSET
        if not isinstance(self.assignee_team_id, Unset):
            assignee_team_id = str(self.assignee_team_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
            }
        )
        if target_issue_id is not UNSET:
            field_dict["target_issue_id"] = target_issue_id
        if member_issue_id is not UNSET:
            field_dict["member_issue_id"] = member_issue_id
        if assignee_account_id is not UNSET:
            field_dict["assignee_account_id"] = assignee_account_id
        if fixed_deployment_id is not UNSET:
            field_dict["fixed_deployment_id"] = fixed_deployment_id
        if ignored_until is not UNSET:
            field_dict["ignored_until"] = ignored_until
        if assignee_team_id is not UNSET:
            field_dict["assignee_team_id"] = assignee_team_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_issue_action_request_action(d.pop("action"))

        _target_issue_id = d.pop("target_issue_id", UNSET)
        target_issue_id: UUID | Unset
        if isinstance(_target_issue_id, Unset):
            target_issue_id = UNSET
        else:
            target_issue_id = UUID(_target_issue_id)

        _member_issue_id = d.pop("member_issue_id", UNSET)
        member_issue_id: UUID | Unset
        if isinstance(_member_issue_id, Unset):
            member_issue_id = UNSET
        else:
            member_issue_id = UUID(_member_issue_id)

        _assignee_account_id = d.pop("assignee_account_id", UNSET)
        assignee_account_id: UUID | Unset
        if isinstance(_assignee_account_id, Unset):
            assignee_account_id = UNSET
        else:
            assignee_account_id = UUID(_assignee_account_id)

        _fixed_deployment_id = d.pop("fixed_deployment_id", UNSET)
        fixed_deployment_id: UUID | Unset
        if isinstance(_fixed_deployment_id, Unset):
            fixed_deployment_id = UNSET
        else:
            fixed_deployment_id = UUID(_fixed_deployment_id)

        _ignored_until = d.pop("ignored_until", UNSET)
        ignored_until: datetime.datetime | Unset
        if isinstance(_ignored_until, Unset):
            ignored_until = UNSET
        else:
            ignored_until = datetime.datetime.fromisoformat(_ignored_until)

        _assignee_team_id = d.pop("assignee_team_id", UNSET)
        assignee_team_id: UUID | Unset
        if isinstance(_assignee_team_id, Unset):
            assignee_team_id = UNSET
        else:
            assignee_team_id = UUID(_assignee_team_id)

        issue_action_request = cls(
            action=action,
            target_issue_id=target_issue_id,
            member_issue_id=member_issue_id,
            assignee_account_id=assignee_account_id,
            fixed_deployment_id=fixed_deployment_id,
            ignored_until=ignored_until,
            assignee_team_id=assignee_team_id,
        )

        issue_action_request.additional_properties = d
        return issue_action_request

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
