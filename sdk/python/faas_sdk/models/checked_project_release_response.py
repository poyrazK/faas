from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.checked_project_release_response_bindings_checks import CheckedProjectReleaseResponseBindingsChecks
    from ..models.project_release_set_response import ProjectReleaseSetResponse


T = TypeVar("T", bound="CheckedProjectReleaseResponse")


@_attrs_define
class CheckedProjectReleaseResponse:
    """Result of a checked project release operation with per-member binding checks."""

    release: ProjectReleaseSetResponse
    """Immutable project deployment graph. Active sets do not expire; when replaced, their TTL starts and
    expires_at is set."""
    bindings_checks: CheckedProjectReleaseResponseBindingsChecks
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        release = self.release.to_dict()

        bindings_checks = self.bindings_checks.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "release": release,
                "bindings_checks": bindings_checks,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.checked_project_release_response_bindings_checks import (
            CheckedProjectReleaseResponseBindingsChecks,
        )
        from ..models.project_release_set_response import ProjectReleaseSetResponse

        d = dict(src_dict)
        release = ProjectReleaseSetResponse.from_dict(d.pop("release"))

        bindings_checks = CheckedProjectReleaseResponseBindingsChecks.from_dict(d.pop("bindings_checks"))

        checked_project_release_response = cls(
            release=release,
            bindings_checks=bindings_checks,
        )

        checked_project_release_response.additional_properties = d
        return checked_project_release_response

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
