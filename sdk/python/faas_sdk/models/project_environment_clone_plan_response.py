from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_environment_clone_plan_action_response import ProjectEnvironmentClonePlanActionResponse


T = TypeVar("T", bound="ProjectEnvironmentClonePlanResponse")


@_attrs_define
class ProjectEnvironmentClonePlanResponse:
    """Read-only, non-secret preflight of one environment clone."""

    project_slug: str
    from_environment: str
    to_environment: str
    share_resources: bool
    can_clone: bool
    """False when the target exists or a managed-resource prerequisite is not met. Quotas are checked again during
    creation."""
    can_promote: bool
    """True when every source workload has a live release available for subsequent promotion."""
    workload_count: int
    actions: list[ProjectEnvironmentClonePlanActionResponse]
    blocking_reasons: list[str]
    warnings: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        from_environment = self.from_environment

        to_environment = self.to_environment

        share_resources = self.share_resources

        can_clone = self.can_clone

        can_promote = self.can_promote

        workload_count = self.workload_count

        actions = []
        for actions_item_data in self.actions:
            actions_item = actions_item_data.to_dict()
            actions.append(actions_item)

        blocking_reasons = self.blocking_reasons

        warnings = self.warnings

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "from_environment": from_environment,
                "to_environment": to_environment,
                "share_resources": share_resources,
                "can_clone": can_clone,
                "can_promote": can_promote,
                "workload_count": workload_count,
                "actions": actions,
                "blocking_reasons": blocking_reasons,
                "warnings": warnings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_clone_plan_action_response import ProjectEnvironmentClonePlanActionResponse

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        from_environment = d.pop("from_environment")

        to_environment = d.pop("to_environment")

        share_resources = d.pop("share_resources")

        can_clone = d.pop("can_clone")

        can_promote = d.pop("can_promote")

        workload_count = d.pop("workload_count")

        actions = []
        _actions = d.pop("actions")
        for actions_item_data in _actions:
            actions_item = ProjectEnvironmentClonePlanActionResponse.from_dict(actions_item_data)

            actions.append(actions_item)

        blocking_reasons = cast(list[str], d.pop("blocking_reasons"))

        warnings = cast(list[str], d.pop("warnings"))

        project_environment_clone_plan_response = cls(
            project_slug=project_slug,
            from_environment=from_environment,
            to_environment=to_environment,
            share_resources=share_resources,
            can_clone=can_clone,
            can_promote=can_promote,
            workload_count=workload_count,
            actions=actions,
            blocking_reasons=blocking_reasons,
            warnings=warnings,
        )

        project_environment_clone_plan_response.additional_properties = d
        return project_environment_clone_plan_response

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
