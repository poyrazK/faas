from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_promotion_control_action import (
    ProjectPromotionControlAction,
    check_project_promotion_control_action,
)
from ..models.project_promotion_control_status import (
    ProjectPromotionControlStatus,
    check_project_promotion_control_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_promotion_control_request import ProjectPromotionControlRequest


T = TypeVar("T", bound="ProjectPromotionControl")


@_attrs_define
class ProjectPromotionControl:
    """Durable promotion abort or restoration decision, including its operator, deadline and resolution."""

    request: ProjectPromotionControlRequest
    """Stable operator request identity and expected promotion checkpoint generation."""
    action: ProjectPromotionControlAction
    status: ProjectPromotionControlStatus
    operator_account_id: str
    requested_at: datetime.datetime
    deadline: datetime.datetime
    resolved_at: datetime.datetime | Unset = UNSET
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request = self.request.to_dict()

        action: str = self.action

        status: str = self.status

        operator_account_id = self.operator_account_id

        requested_at = self.requested_at.isoformat()

        deadline = self.deadline.isoformat()

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request": request,
                "action": action,
                "status": status,
                "operator_account_id": operator_account_id,
                "requested_at": requested_at,
                "deadline": deadline,
            }
        )
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_promotion_control_request import ProjectPromotionControlRequest

        d = dict(src_dict)
        request = ProjectPromotionControlRequest.from_dict(d.pop("request"))

        action = check_project_promotion_control_action(d.pop("action"))

        status = check_project_promotion_control_status(d.pop("status"))

        operator_account_id = d.pop("operator_account_id")

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        deadline = datetime.datetime.fromisoformat(d.pop("deadline"))

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        reason = d.pop("reason", UNSET)

        project_promotion_control = cls(
            request=request,
            action=action,
            status=status,
            operator_account_id=operator_account_id,
            requested_at=requested_at,
            deadline=deadline,
            resolved_at=resolved_at,
            reason=reason,
        )

        project_promotion_control.additional_properties = d
        return project_promotion_control

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
