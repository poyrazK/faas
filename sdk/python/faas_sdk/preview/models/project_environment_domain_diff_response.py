from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_domain_diff_response_kind import (
    ProjectEnvironmentDomainDiffResponseKind,
    check_project_environment_domain_diff_response_kind,
)

if TYPE_CHECKING:
    from ..models.project_environment_domain_response import ProjectEnvironmentDomainResponse


T = TypeVar("T", bound="ProjectEnvironmentDomainDiffResponse")


@_attrs_define
class ProjectEnvironmentDomainDiffResponse:
    """Environment-owned hostname changes for one workload."""

    kind: ProjectEnvironmentDomainDiffResponseKind
    before: list[ProjectEnvironmentDomainResponse]
    after: list[ProjectEnvironmentDomainResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        before = []
        for before_item_data in self.before:
            before_item = before_item_data.to_dict()
            before.append(before_item)

        after = []
        for after_item_data in self.after:
            after_item = after_item_data.to_dict()
            after.append(after_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "before": before,
                "after": after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_domain_response import ProjectEnvironmentDomainResponse

        d = dict(src_dict)
        kind = check_project_environment_domain_diff_response_kind(d.pop("kind"))

        before = []
        _before = d.pop("before")
        for before_item_data in _before:
            before_item = ProjectEnvironmentDomainResponse.from_dict(before_item_data)

            before.append(before_item)

        after = []
        _after = d.pop("after")
        for after_item_data in _after:
            after_item = ProjectEnvironmentDomainResponse.from_dict(after_item_data)

            after.append(after_item)

        project_environment_domain_diff_response = cls(
            kind=kind,
            before=before,
            after=after,
        )

        project_environment_domain_diff_response.additional_properties = d
        return project_environment_domain_diff_response

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
