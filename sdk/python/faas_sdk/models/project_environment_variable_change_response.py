from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_variable_change_response_kind import (
    ProjectEnvironmentVariableChangeResponseKind,
    check_project_environment_variable_change_response_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentVariableChangeResponse")


@_attrs_define
class ProjectEnvironmentVariableChangeResponse:
    """Non-secret runtime variable change between two environments."""

    key: str
    kind: ProjectEnvironmentVariableChangeResponseKind
    before: str | Unset = UNSET
    after: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        kind: str = self.kind

        before = self.before

        after = self.after

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
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
        d = dict(src_dict)
        key = d.pop("key")

        kind = check_project_environment_variable_change_response_kind(d.pop("kind"))

        before = d.pop("before", UNSET)

        after = d.pop("after", UNSET)

        project_environment_variable_change_response = cls(
            key=key,
            kind=kind,
            before=before,
            after=after,
        )

        project_environment_variable_change_response.additional_properties = d
        return project_environment_variable_change_response

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
