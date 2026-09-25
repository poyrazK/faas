from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_domain_change_response_kind import (
    ProjectEnvironmentDomainChangeResponseKind,
    check_project_environment_domain_change_response_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_domain_response import ProjectEnvironmentDomainResponse


T = TypeVar("T", bound="ProjectEnvironmentDomainChangeResponse")


@_attrs_define
class ProjectEnvironmentDomainChangeResponse:
    """A hostname added to, removed from, or changed between two environments."""

    domain: str
    kind: ProjectEnvironmentDomainChangeResponseKind
    before: ProjectEnvironmentDomainResponse | Unset = UNSET
    """Effective custom hostname and its ownership for one workload."""
    after: ProjectEnvironmentDomainResponse | Unset = UNSET
    """Effective custom hostname and its ownership for one workload."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        kind: str = self.kind

        before: dict[str, Any] | Unset = UNSET
        if not isinstance(self.before, Unset):
            before = self.before.to_dict()

        after: dict[str, Any] | Unset = UNSET
        if not isinstance(self.after, Unset):
            after = self.after.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "domain": domain,
                "kind": kind,
            }
        )
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_domain_response import ProjectEnvironmentDomainResponse

        d = dict(src_dict)
        domain = d.pop("domain")

        kind = check_project_environment_domain_change_response_kind(d.pop("kind"))

        _before = d.pop("before", UNSET)
        before: ProjectEnvironmentDomainResponse | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = ProjectEnvironmentDomainResponse.from_dict(_before)

        _after = d.pop("after", UNSET)
        after: ProjectEnvironmentDomainResponse | Unset
        if isinstance(_after, Unset):
            after = UNSET
        else:
            after = ProjectEnvironmentDomainResponse.from_dict(_after)

        project_environment_domain_change_response = cls(
            domain=domain,
            kind=kind,
            before=before,
            after=after,
        )

        project_environment_domain_change_response.additional_properties = d
        return project_environment_domain_change_response

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
