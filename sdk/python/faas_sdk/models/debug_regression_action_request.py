from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_regression_action_request_action import (
    DebugRegressionActionRequestAction,
    check_debug_regression_action_request_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugRegressionActionRequest")


@_attrs_define
class DebugRegressionActionRequest:
    deployment_id: UUID
    route: str
    action: DebugRegressionActionRequestAction
    dismissed_until: datetime.datetime | Unset = UNSET
    """Dismissal expiry; defaults to 24h and is capped at 30d."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        route = self.route

        action: str = self.action

        dismissed_until: str | Unset = UNSET
        if not isinstance(self.dismissed_until, Unset):
            dismissed_until = self.dismissed_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "route": route,
                "action": action,
            }
        )
        if dismissed_until is not UNSET:
            field_dict["dismissed_until"] = dismissed_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        route = d.pop("route")

        action = check_debug_regression_action_request_action(d.pop("action"))

        _dismissed_until = d.pop("dismissed_until", UNSET)
        dismissed_until: datetime.datetime | Unset
        if isinstance(_dismissed_until, Unset):
            dismissed_until = UNSET
        else:
            dismissed_until = datetime.datetime.fromisoformat(_dismissed_until)

        debug_regression_action_request = cls(
            deployment_id=deployment_id,
            route=route,
            action=action,
            dismissed_until=dismissed_until,
        )

        debug_regression_action_request.additional_properties = d
        return debug_regression_action_request

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
