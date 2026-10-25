from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.job_response import JobResponse


T = TypeVar("T", bound="ListJobsResponse")


@_attrs_define
class ListJobsResponse:
    """Page of jobs on the account (issue"""

    jobs: list[JobResponse]
    limit: int
    offset: int
    next_offset: int
    """-1 = last page"""
    total: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        jobs = []
        for jobs_item_data in self.jobs:
            jobs_item = jobs_item_data.to_dict()
            jobs.append(jobs_item)

        limit = self.limit

        offset = self.offset

        next_offset = self.next_offset

        total = self.total

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "jobs": jobs,
                "limit": limit,
                "offset": offset,
                "next_offset": next_offset,
                "total": total,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.job_response import JobResponse

        d = dict(src_dict)
        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = JobResponse.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        limit = d.pop("limit")

        offset = d.pop("offset")

        next_offset = d.pop("next_offset")

        total = d.pop("total")

        list_jobs_response = cls(
            jobs=jobs,
            limit=limit,
            offset=offset,
            next_offset=next_offset,
            total=total,
        )

        list_jobs_response.additional_properties = d
        return list_jobs_response

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
