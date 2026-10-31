from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.bulk_issue_lifecycle_action_action import (
    BulkIssueLifecycleActionAction,
    check_bulk_issue_lifecycle_action_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="BulkIssueLifecycleAction")


@_attrs_define
class BulkIssueLifecycleAction:
    """Lifecycle or assignment action applied atomically to the selected canonical issue groups."""

    action: BulkIssueLifecycleActionAction
    assignee_account_id: UUID | Unset = UNSET
    """Only for assign; omit both assignment targets to unassign."""
    fixed_deployment_id: UUID | Unset = UNSET
    """Required only for resolve."""
    ignored_until: datetime.datetime | Unset = UNSET
    """Required only for ignore; future and within 90 days."""
    assignee_team_id: UUID | Unset = UNSET
    """Only for assign; mutually exclusive with assignee_account_id."""

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

        assignee_team_id: str | Unset = UNSET
        if not isinstance(self.assignee_team_id, Unset):
            assignee_team_id = str(self.assignee_team_id)

        field_dict: dict[str, Any] = {}

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
        if assignee_team_id is not UNSET:
            field_dict["assignee_team_id"] = assignee_team_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_bulk_issue_lifecycle_action_action(d.pop("action"))

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

        bulk_issue_lifecycle_action = cls(
            action=action,
            assignee_account_id=assignee_account_id,
            fixed_deployment_id=fixed_deployment_id,
            ignored_until=ignored_until,
            assignee_team_id=assignee_team_id,
        )

        return bulk_issue_lifecycle_action
