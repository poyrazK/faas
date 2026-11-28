from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_blocker_priority import (
    OperationWorkflowBlockerPriority,
    check_operation_workflow_blocker_priority,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowBlocker")


@_attrs_define
class OperationWorkflowBlocker:
    """Public application-reported reason a named target Operation must wait. Reports replace the entire prior blocker list
    at their state revision; these observations do not grant or enforce execution authority.

    """

    code: str
    description: str
    """Public UTF-8 text limited to 512 bytes without control characters."""
    operation: str
    """Target Operation name."""
    priority: OperationWorkflowBlockerPriority | Unset = UNSET
    """Application-assigned urgency. Omitted means normal for queue filtering and counts."""
    business_impact: str | Unset = UNSET
    """Public application-reported impact limited to 512 UTF-8 bytes without control characters."""
    acknowledged_at: datetime.datetime | Unset = UNSET
    """Application acknowledgement time paired with acknowledged_by. Must be at or after known first observation
    and at or before report time."""
    acknowledged_by: str | Unset = UNSET
    """Public application actor identifier paired with acknowledged_at. Limited to 128 UTF-8 bytes without control
    characters."""
    follow_up_at: datetime.datetime | Unset = UNSET
    """Optional follow-up deadline at or after acknowledgement. Does not resolve the blocker or reset its age."""
    owner: str | Unset = UNSET
    """Optional public application-assigned person or team identifier, limited to 128 UTF-8 bytes without control
    characters. Empty or omitted means unassigned; this grants no execution authority."""
    next_action: str | Unset = UNSET
    """Optional public application-suggested resolution step, limited to 512 UTF-8 bytes without control
    characters. This is guidance rather than an executable command."""
    first_observed_at: datetime.datetime | Unset = UNSET
    """Optional application observation time at or before the containing report. Upgraded transactional SDKs
    preserve it across repeats of the same target/code until cleared. Omitted means unknown."""

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        description = self.description

        operation = self.operation

        priority: str | Unset = UNSET
        if not isinstance(self.priority, Unset):
            priority = self.priority

        business_impact = self.business_impact

        acknowledged_at: str | Unset = UNSET
        if not isinstance(self.acknowledged_at, Unset):
            acknowledged_at = self.acknowledged_at.isoformat()

        acknowledged_by = self.acknowledged_by

        follow_up_at: str | Unset = UNSET
        if not isinstance(self.follow_up_at, Unset):
            follow_up_at = self.follow_up_at.isoformat()

        owner = self.owner

        next_action = self.next_action

        first_observed_at: str | Unset = UNSET
        if not isinstance(self.first_observed_at, Unset):
            first_observed_at = self.first_observed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "description": description,
                "operation": operation,
            }
        )
        if priority is not UNSET:
            field_dict["priority"] = priority
        if business_impact is not UNSET:
            field_dict["business_impact"] = business_impact
        if acknowledged_at is not UNSET:
            field_dict["acknowledged_at"] = acknowledged_at
        if acknowledged_by is not UNSET:
            field_dict["acknowledged_by"] = acknowledged_by
        if follow_up_at is not UNSET:
            field_dict["follow_up_at"] = follow_up_at
        if owner is not UNSET:
            field_dict["owner"] = owner
        if next_action is not UNSET:
            field_dict["next_action"] = next_action
        if first_observed_at is not UNSET:
            field_dict["first_observed_at"] = first_observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        description = d.pop("description")

        operation = d.pop("operation")

        _priority = d.pop("priority", UNSET)
        priority: OperationWorkflowBlockerPriority | Unset
        if isinstance(_priority, Unset):
            priority = UNSET
        else:
            priority = check_operation_workflow_blocker_priority(_priority)

        business_impact = d.pop("business_impact", UNSET)

        _acknowledged_at = d.pop("acknowledged_at", UNSET)
        acknowledged_at: datetime.datetime | Unset
        if isinstance(_acknowledged_at, Unset):
            acknowledged_at = UNSET
        else:
            acknowledged_at = datetime.datetime.fromisoformat(_acknowledged_at)

        acknowledged_by = d.pop("acknowledged_by", UNSET)

        _follow_up_at = d.pop("follow_up_at", UNSET)
        follow_up_at: datetime.datetime | Unset
        if isinstance(_follow_up_at, Unset):
            follow_up_at = UNSET
        else:
            follow_up_at = datetime.datetime.fromisoformat(_follow_up_at)

        owner = d.pop("owner", UNSET)

        next_action = d.pop("next_action", UNSET)

        _first_observed_at = d.pop("first_observed_at", UNSET)
        first_observed_at: datetime.datetime | Unset
        if isinstance(_first_observed_at, Unset):
            first_observed_at = UNSET
        else:
            first_observed_at = datetime.datetime.fromisoformat(_first_observed_at)

        operation_workflow_blocker = cls(
            code=code,
            description=description,
            operation=operation,
            priority=priority,
            business_impact=business_impact,
            acknowledged_at=acknowledged_at,
            acknowledged_by=acknowledged_by,
            follow_up_at=follow_up_at,
            owner=owner,
            next_action=next_action,
            first_observed_at=first_observed_at,
        )

        return operation_workflow_blocker
