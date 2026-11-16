from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentBranchDatabase")


@_attrs_define
class ProjectEnvironmentBranchDatabase:
    """One managed PostgreSQL copy a branch owns."""

    source_database_id: str
    name: str
    copy_database_id: str | Unset = UNSET
    """Empty until the copy is made."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_database_id = self.source_database_id

        name = self.name

        copy_database_id = self.copy_database_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_database_id": source_database_id,
                "name": name,
            }
        )
        if copy_database_id is not UNSET:
            field_dict["copy_database_id"] = copy_database_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_database_id = d.pop("source_database_id")

        name = d.pop("name")

        copy_database_id = d.pop("copy_database_id", UNSET)

        project_environment_branch_database = cls(
            source_database_id=source_database_id,
            name=name,
            copy_database_id=copy_database_id,
        )

        project_environment_branch_database.additional_properties = d
        return project_environment_branch_database

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
