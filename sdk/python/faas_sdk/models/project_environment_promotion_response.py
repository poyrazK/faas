from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_promotion_release_graph_response import (
        ProjectEnvironmentPromotionReleaseGraphResponse,
    )
    from ..models.project_environment_promotion_workload_response import ProjectEnvironmentPromotionWorkloadResponse


T = TypeVar("T", bound="ProjectEnvironmentPromotionResponse")


@_attrs_define
class ProjectEnvironmentPromotionResponse:
    """Result of a guarded project environment promotion."""

    promotion_id: str
    project_slug: str
    from_environment: str
    to_environment: str
    promotion_hash: str
    workloads: list[ProjectEnvironmentPromotionWorkloadResponse]
    sync_config: bool | Unset = UNSET
    """Whether this promotion copied the source's non-secret configuration."""
    release_graph: ProjectEnvironmentPromotionReleaseGraphResponse | Unset = UNSET
    """Immutable graph identities involved in a graph-aware promotion and rollback."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        promotion_id = self.promotion_id

        project_slug = self.project_slug

        from_environment = self.from_environment

        to_environment = self.to_environment

        promotion_hash = self.promotion_hash

        workloads = []
        for workloads_item_data in self.workloads:
            workloads_item = workloads_item_data.to_dict()
            workloads.append(workloads_item)

        sync_config = self.sync_config

        release_graph: dict[str, Any] | Unset = UNSET
        if not isinstance(self.release_graph, Unset):
            release_graph = self.release_graph.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "promotion_id": promotion_id,
                "project_slug": project_slug,
                "from_environment": from_environment,
                "to_environment": to_environment,
                "promotion_hash": promotion_hash,
                "workloads": workloads,
            }
        )
        if sync_config is not UNSET:
            field_dict["sync_config"] = sync_config
        if release_graph is not UNSET:
            field_dict["release_graph"] = release_graph

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_promotion_release_graph_response import (
            ProjectEnvironmentPromotionReleaseGraphResponse,
        )
        from ..models.project_environment_promotion_workload_response import ProjectEnvironmentPromotionWorkloadResponse

        d = dict(src_dict)
        promotion_id = d.pop("promotion_id")

        project_slug = d.pop("project_slug")

        from_environment = d.pop("from_environment")

        to_environment = d.pop("to_environment")

        promotion_hash = d.pop("promotion_hash")

        workloads = []
        _workloads = d.pop("workloads")
        for workloads_item_data in _workloads:
            workloads_item = ProjectEnvironmentPromotionWorkloadResponse.from_dict(workloads_item_data)

            workloads.append(workloads_item)

        sync_config = d.pop("sync_config", UNSET)

        _release_graph = d.pop("release_graph", UNSET)
        release_graph: ProjectEnvironmentPromotionReleaseGraphResponse | Unset
        if isinstance(_release_graph, Unset):
            release_graph = UNSET
        else:
            release_graph = ProjectEnvironmentPromotionReleaseGraphResponse.from_dict(_release_graph)

        project_environment_promotion_response = cls(
            promotion_id=promotion_id,
            project_slug=project_slug,
            from_environment=from_environment,
            to_environment=to_environment,
            promotion_hash=promotion_hash,
            workloads=workloads,
            sync_config=sync_config,
            release_graph=release_graph,
        )

        project_environment_promotion_response.additional_properties = d
        return project_environment_promotion_response

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
