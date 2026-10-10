from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_dependency_latency_item import DebugDependencyLatencyItem


T = TypeVar("T", bound="DebugDependencyDeploymentComparison")


@_attrs_define
class DebugDependencyDeploymentComparison:
    """Dependency latency split by deployment (ADR-934): baseline_* fields describe the previous deployment, current_* the
    compared one. Regressions first, then by current p95.

    """

    current_deployment_id: str
    previous_deployment_id: str
    dependencies: list[DebugDependencyLatencyItem]
    truncated: bool
    current_deployment_tag: str | Unset = UNSET
    current_commit_sha: str | Unset = UNSET
    previous_deployment_tag: str | Unset = UNSET
    previous_commit_sha: str | Unset = UNSET
    route: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        current_deployment_id = self.current_deployment_id

        previous_deployment_id = self.previous_deployment_id

        dependencies = []
        for dependencies_item_data in self.dependencies:
            dependencies_item = dependencies_item_data.to_dict()
            dependencies.append(dependencies_item)

        truncated = self.truncated

        current_deployment_tag = self.current_deployment_tag

        current_commit_sha = self.current_commit_sha

        previous_deployment_tag = self.previous_deployment_tag

        previous_commit_sha = self.previous_commit_sha

        route = self.route

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "current_deployment_id": current_deployment_id,
                "previous_deployment_id": previous_deployment_id,
                "dependencies": dependencies,
                "truncated": truncated,
            }
        )
        if current_deployment_tag is not UNSET:
            field_dict["current_deployment_tag"] = current_deployment_tag
        if current_commit_sha is not UNSET:
            field_dict["current_commit_sha"] = current_commit_sha
        if previous_deployment_tag is not UNSET:
            field_dict["previous_deployment_tag"] = previous_deployment_tag
        if previous_commit_sha is not UNSET:
            field_dict["previous_commit_sha"] = previous_commit_sha
        if route is not UNSET:
            field_dict["route"] = route

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_dependency_latency_item import DebugDependencyLatencyItem

        d = dict(src_dict)
        current_deployment_id = d.pop("current_deployment_id")

        previous_deployment_id = d.pop("previous_deployment_id")

        dependencies = []
        _dependencies = d.pop("dependencies")
        for dependencies_item_data in _dependencies:
            dependencies_item = DebugDependencyLatencyItem.from_dict(dependencies_item_data)

            dependencies.append(dependencies_item)

        truncated = d.pop("truncated")

        current_deployment_tag = d.pop("current_deployment_tag", UNSET)

        current_commit_sha = d.pop("current_commit_sha", UNSET)

        previous_deployment_tag = d.pop("previous_deployment_tag", UNSET)

        previous_commit_sha = d.pop("previous_commit_sha", UNSET)

        route = d.pop("route", UNSET)

        debug_dependency_deployment_comparison = cls(
            current_deployment_id=current_deployment_id,
            previous_deployment_id=previous_deployment_id,
            dependencies=dependencies,
            truncated=truncated,
            current_deployment_tag=current_deployment_tag,
            current_commit_sha=current_commit_sha,
            previous_deployment_tag=previous_deployment_tag,
            previous_commit_sha=previous_commit_sha,
            route=route,
        )

        debug_dependency_deployment_comparison.additional_properties = d
        return debug_dependency_deployment_comparison

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
