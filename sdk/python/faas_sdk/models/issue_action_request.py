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
    """Desired ownership or lifecycle action, validated within the app scope."""

    action: IssueActionRequestAction
    assignee_account_id: UUID | Unset = UNSET
    fixed_deployment_id: UUID | Unset = UNSET
    ignored_until: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        assignee_account_id: str | Unset = UNSET
        if not isinstance(self.assignee_account_id, Unset):
            assignee_account_id = str(self.assignee_account_id)

        fixed_deployment_id: str | Unset = UNSET
        if not isinstance(self.fixed_deployment_id, Unset):
            fixed_deployment_id = str(self.fixed_deployment_id)

        ignored_until: str | Unset = UNSET
        if not isinstance(self.ignored_until, Unset):
            ignored_until = self.ignored_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
            }
        )
        if assignee_account_id is not UNSET:
            field_dict["assignee_account_id"] = assignee_account_id
        if fixed_deployment_id is not UNSET:
            field_dict["fixed_deployment_id"] = fixed_deployment_id
        if ignored_until is not UNSET:
            field_dict["ignored_until"] = ignored_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_issue_action_request_action(d.pop("action"))

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

        issue_action_request = cls(
            action=action,
            assignee_account_id=assignee_account_id,
            fixed_deployment_id=fixed_deployment_id,
            ignored_until=ignored_until,
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
