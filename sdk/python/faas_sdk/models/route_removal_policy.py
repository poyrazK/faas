from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_removal_policy_mode import RouteRemovalPolicyMode, check_route_removal_policy_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteRemovalPolicy")


@_attrs_define
class RouteRemovalPolicy:
    app_id: UUID
    mode: RouteRemovalPolicyMode
    revision: int
    grace_period: str
    """Go duration; default 720h."""
    max_approval_age: str
    """Go duration; default 1h."""
    baseline_deployment_id: UUID | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        mode: str = self.mode

        revision = self.revision

        grace_period = self.grace_period

        max_approval_age = self.max_approval_age

        baseline_deployment_id: str | Unset = UNSET
        if not isinstance(self.baseline_deployment_id, Unset):
            baseline_deployment_id = str(self.baseline_deployment_id)

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "mode": mode,
                "revision": revision,
                "grace_period": grace_period,
                "max_approval_age": max_approval_age,
            }
        )
        if baseline_deployment_id is not UNSET:
            field_dict["baseline_deployment_id"] = baseline_deployment_id
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        mode = check_route_removal_policy_mode(d.pop("mode"))

        revision = d.pop("revision")

        grace_period = d.pop("grace_period")

        max_approval_age = d.pop("max_approval_age")

        _baseline_deployment_id = d.pop("baseline_deployment_id", UNSET)
        baseline_deployment_id: UUID | Unset
        if isinstance(_baseline_deployment_id, Unset):
            baseline_deployment_id = UNSET
        else:
            baseline_deployment_id = UUID(_baseline_deployment_id)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        route_removal_policy = cls(
            app_id=app_id,
            mode=mode,
            revision=revision,
            grace_period=grace_period,
            max_approval_age=max_approval_age,
            baseline_deployment_id=baseline_deployment_id,
            updated_at=updated_at,
        )

        route_removal_policy.additional_properties = d
        return route_removal_policy

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
