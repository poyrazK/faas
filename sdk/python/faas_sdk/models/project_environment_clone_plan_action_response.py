from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_clone_plan_action_response_action import (
    ProjectEnvironmentClonePlanActionResponseAction,
    check_project_environment_clone_plan_action_response_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentClonePlanActionResponse")


@_attrs_define
class ProjectEnvironmentClonePlanActionResponse:
    """Non-secret summary of how one resource behaves during a clone."""

    resource: str
    action: ProjectEnvironmentClonePlanActionResponseAction
    workload_slug: str | Unset = UNSET
    count: int | Unset = UNSET
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        resource = self.resource

        action: str = self.action

        workload_slug = self.workload_slug

        count = self.count

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "resource": resource,
                "action": action,
            }
        )
        if workload_slug is not UNSET:
            field_dict["workload_slug"] = workload_slug
        if count is not UNSET:
            field_dict["count"] = count
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        resource = d.pop("resource")

        action = check_project_environment_clone_plan_action_response_action(d.pop("action"))

        workload_slug = d.pop("workload_slug", UNSET)

        count = d.pop("count", UNSET)

        reason = d.pop("reason", UNSET)

        project_environment_clone_plan_action_response = cls(
            resource=resource,
            action=action,
            workload_slug=workload_slug,
            count=count,
            reason=reason,
        )

        project_environment_clone_plan_action_response.additional_properties = d
        return project_environment_clone_plan_action_response

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
