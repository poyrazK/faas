from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_workload_response import ProjectWorkloadResponse


T = TypeVar("T", bound="ProjectResponse")


@_attrs_define
class ProjectResponse:
    """Project metadata with its workloads, exclusions, and latest reconciliation state."""

    id: str
    slug: str
    scan_source: str
    workload_count: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    workloads: list[ProjectWorkloadResponse]
    exclusions: list[str]
    repo_full_name: str | Unset = UNSET
    production_branch: str | Unset = UNSET
    last_reconciliation_status: str | Unset = UNSET
    last_build_status: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        slug = self.slug

        scan_source = self.scan_source

        workload_count = self.workload_count

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        workloads = []
        for workloads_item_data in self.workloads:
            workloads_item = workloads_item_data.to_dict()
            workloads.append(workloads_item)

        exclusions = self.exclusions

        repo_full_name = self.repo_full_name

        production_branch = self.production_branch

        last_reconciliation_status = self.last_reconciliation_status

        last_build_status = self.last_build_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "slug": slug,
                "scan_source": scan_source,
                "workload_count": workload_count,
                "created_at": created_at,
                "updated_at": updated_at,
                "workloads": workloads,
                "exclusions": exclusions,
            }
        )
        if repo_full_name is not UNSET:
            field_dict["repo_full_name"] = repo_full_name
        if production_branch is not UNSET:
            field_dict["production_branch"] = production_branch
        if last_reconciliation_status is not UNSET:
            field_dict["last_reconciliation_status"] = last_reconciliation_status
        if last_build_status is not UNSET:
            field_dict["last_build_status"] = last_build_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_workload_response import ProjectWorkloadResponse

        d = dict(src_dict)
        id = d.pop("id")

        slug = d.pop("slug")

        scan_source = d.pop("scan_source")

        workload_count = d.pop("workload_count")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        workloads = []
        _workloads = d.pop("workloads")
        for workloads_item_data in _workloads:
            workloads_item = ProjectWorkloadResponse.from_dict(workloads_item_data)

            workloads.append(workloads_item)

        exclusions = cast(list[str], d.pop("exclusions"))

        repo_full_name = d.pop("repo_full_name", UNSET)

        production_branch = d.pop("production_branch", UNSET)

        last_reconciliation_status = d.pop("last_reconciliation_status", UNSET)

        last_build_status = d.pop("last_build_status", UNSET)

        project_response = cls(
            id=id,
            slug=slug,
            scan_source=scan_source,
            workload_count=workload_count,
            created_at=created_at,
            updated_at=updated_at,
            workloads=workloads,
            exclusions=exclusions,
            repo_full_name=repo_full_name,
            production_branch=production_branch,
            last_reconciliation_status=last_reconciliation_status,
            last_build_status=last_build_status,
        )

        project_response.additional_properties = d
        return project_response

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
