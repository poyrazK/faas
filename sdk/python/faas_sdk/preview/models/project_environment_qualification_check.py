from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.project_environment_qualification_check_name import (
    ProjectEnvironmentQualificationCheckName,
    check_project_environment_qualification_check_name,
)
from ..models.project_environment_qualification_check_status import (
    ProjectEnvironmentQualificationCheckStatus,
    check_project_environment_qualification_check_status,
)

if TYPE_CHECKING:
    from ..models.project_environment_qualification_result import ProjectEnvironmentQualificationResult


T = TypeVar("T", bound="ProjectEnvironmentQualificationCheck")


@_attrs_define
class ProjectEnvironmentQualificationCheck:
    """Aggregate result plus one exact-deployment probe outcome per workload."""

    name: ProjectEnvironmentQualificationCheckName
    status: ProjectEnvironmentQualificationCheckStatus
    results: list[ProjectEnvironmentQualificationResult]

    def to_dict(self) -> dict[str, Any]:
        name: str = self.name

        status: str = self.status

        results = []
        for results_item_data in self.results:
            results_item = results_item_data.to_dict()
            results.append(results_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "status": status,
                "results": results,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_qualification_result import ProjectEnvironmentQualificationResult

        d = dict(src_dict)
        name = check_project_environment_qualification_check_name(d.pop("name"))

        status = check_project_environment_qualification_check_status(d.pop("status"))

        results = []
        _results = d.pop("results")
        for results_item_data in _results:
            results_item = ProjectEnvironmentQualificationResult.from_dict(results_item_data)

            results.append(results_item)

        project_environment_qualification_check = cls(
            name=name,
            status=status,
            results=results,
        )

        return project_environment_qualification_check
