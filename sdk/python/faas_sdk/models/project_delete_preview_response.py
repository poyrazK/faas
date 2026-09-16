from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_summary_response import ProjectSummaryResponse
    from ..models.project_workload_response import ProjectWorkloadResponse


T = TypeVar("T", bound="ProjectDeletePreviewResponse")


@_attrs_define
class ProjectDeletePreviewResponse:
    """Workloads and related resources that remain live when a project is deleted."""

    project: ProjectSummaryResponse
    """Stable account-scoped project list item."""
    workloads: list[ProjectWorkloadResponse]
    domain_count: int
    env_count: int
    cron_count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project = self.project.to_dict()

        workloads = []
        for workloads_item_data in self.workloads:
            workloads_item = workloads_item_data.to_dict()
            workloads.append(workloads_item)

        domain_count = self.domain_count

        env_count = self.env_count

        cron_count = self.cron_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project": project,
                "workloads": workloads,
                "domain_count": domain_count,
                "env_count": env_count,
                "cron_count": cron_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_summary_response import ProjectSummaryResponse
        from ..models.project_workload_response import ProjectWorkloadResponse

        d = dict(src_dict)
        project = ProjectSummaryResponse.from_dict(d.pop("project"))

        workloads = []
        _workloads = d.pop("workloads")
        for workloads_item_data in _workloads:
            workloads_item = ProjectWorkloadResponse.from_dict(workloads_item_data)

            workloads.append(workloads_item)

        domain_count = d.pop("domain_count")

        env_count = d.pop("env_count")

        cron_count = d.pop("cron_count")

        project_delete_preview_response = cls(
            project=project,
            workloads=workloads,
            domain_count=domain_count,
            env_count=env_count,
            cron_count=cron_count,
        )

        project_delete_preview_response.additional_properties = d
        return project_delete_preview_response

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
