from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectSummaryResponse")


@_attrs_define
class ProjectSummaryResponse:
    """Project list item with its persisted organization owner."""

    id: str
    slug: str
    scan_source: str
    workload_count: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    org_id: UUID | Unset = UNSET
    repo_full_name: str | Unset = UNSET
    production_branch: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        slug = self.slug

        scan_source = self.scan_source

        workload_count = self.workload_count

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        org_id: str | Unset = UNSET
        if not isinstance(self.org_id, Unset):
            org_id = str(self.org_id)

        repo_full_name = self.repo_full_name

        production_branch = self.production_branch

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
            }
        )
        if org_id is not UNSET:
            field_dict["org_id"] = org_id
        if repo_full_name is not UNSET:
            field_dict["repo_full_name"] = repo_full_name
        if production_branch is not UNSET:
            field_dict["production_branch"] = production_branch

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        slug = d.pop("slug")

        scan_source = d.pop("scan_source")

        workload_count = d.pop("workload_count")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _org_id = d.pop("org_id", UNSET)
        org_id: UUID | Unset
        if isinstance(_org_id, Unset):
            org_id = UNSET
        else:
            org_id = UUID(_org_id)

        repo_full_name = d.pop("repo_full_name", UNSET)

        production_branch = d.pop("production_branch", UNSET)

        project_summary_response = cls(
            id=id,
            slug=slug,
            scan_source=scan_source,
            workload_count=workload_count,
            created_at=created_at,
            updated_at=updated_at,
            org_id=org_id,
            repo_full_name=repo_full_name,
            production_branch=production_branch,
        )

        project_summary_response.additional_properties = d
        return project_summary_response

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
