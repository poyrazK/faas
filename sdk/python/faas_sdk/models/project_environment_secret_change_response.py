from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_secret_change_response_kind import (
    ProjectEnvironmentSecretChangeResponseKind,
    check_project_environment_secret_change_response_kind,
)

if TYPE_CHECKING:
    from ..models.project_environment_secret_cell_response import ProjectEnvironmentSecretCellResponse


T = TypeVar("T", bound="ProjectEnvironmentSecretChangeResponse")


@_attrs_define
class ProjectEnvironmentSecretChangeResponse:
    """Secret metadata comparison that excludes secret values and ciphertext."""

    key: str
    kind: ProjectEnvironmentSecretChangeResponseKind
    before: ProjectEnvironmentSecretCellResponse
    """One side of a secret comparison; never contains secret material."""
    after: ProjectEnvironmentSecretCellResponse
    """One side of a secret comparison; never contains secret material."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        kind: str = self.kind

        before = self.before.to_dict()

        after = self.after.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "kind": kind,
                "before": before,
                "after": after,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_secret_cell_response import ProjectEnvironmentSecretCellResponse

        d = dict(src_dict)
        key = d.pop("key")

        kind = check_project_environment_secret_change_response_kind(d.pop("kind"))

        before = ProjectEnvironmentSecretCellResponse.from_dict(d.pop("before"))

        after = ProjectEnvironmentSecretCellResponse.from_dict(d.pop("after"))

        project_environment_secret_change_response = cls(
            key=key,
            kind=kind,
            before=before,
            after=after,
        )

        project_environment_secret_change_response.additional_properties = d
        return project_environment_secret_change_response

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
