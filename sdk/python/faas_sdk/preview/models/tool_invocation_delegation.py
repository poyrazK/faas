from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolInvocationDelegation")


@_attrs_define
class ToolInvocationDelegation:
    """Immutable authority resolved by APID; never accepted as caller-supplied invocation metadata."""

    grant_id: UUID
    key_id: str
    project_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grant_id = str(self.grant_id)

        key_id = self.key_id

        project_id = self.project_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grant_id": grant_id,
                "key_id": key_id,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        grant_id = UUID(d.pop("grant_id"))

        key_id = d.pop("key_id")

        project_id = d.pop("project_id", UNSET)

        tool_invocation_delegation = cls(
            grant_id=grant_id,
            key_id=key_id,
            project_id=project_id,
        )

        tool_invocation_delegation.additional_properties = d
        return tool_invocation_delegation

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
