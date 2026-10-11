from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_source_location import ProfileSourceLocation


T = TypeVar("T", bound="ProfileStack")


@_attrs_define
class ProfileStack:
    """One frame in a bounded CPU call-path tree."""

    name: str
    cpu_seconds: float
    file: str | Unset = UNSET
    line: int | Unset = UNSET
    source: ProfileSourceLocation | Unset = UNSET
    """Safely mapped sampled source line at the deployment's recorded commit. Omitted for missing symbols, unknown
    paths, generated adapters, or exhausted link bounds. Private repository access is enforced by GitHub."""
    children: list[ProfileStack] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        cpu_seconds = self.cpu_seconds

        file = self.file

        line = self.line

        source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source.to_dict()

        children: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.children, Unset):
            children = []
            for children_item_data in self.children:
                children_item = children_item_data.to_dict()
                children.append(children_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "cpu_seconds": cpu_seconds,
            }
        )
        if file is not UNSET:
            field_dict["file"] = file
        if line is not UNSET:
            field_dict["line"] = line
        if source is not UNSET:
            field_dict["source"] = source
        if children is not UNSET:
            field_dict["children"] = children

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_source_location import ProfileSourceLocation

        d = dict(src_dict)
        name = d.pop("name")

        cpu_seconds = d.pop("cpu_seconds")

        file = d.pop("file", UNSET)

        line = d.pop("line", UNSET)

        _source = d.pop("source", UNSET)
        source: ProfileSourceLocation | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = ProfileSourceLocation.from_dict(_source)

        _children = d.pop("children", UNSET)
        children: list[ProfileStack] | Unset = UNSET
        if _children is not UNSET:
            children = []
            for children_item_data in _children:
                children_item = ProfileStack.from_dict(children_item_data)

                children.append(children_item)

        profile_stack = cls(
            name=name,
            cpu_seconds=cpu_seconds,
            file=file,
            line=line,
            source=source,
            children=children,
        )

        profile_stack.additional_properties = d
        return profile_stack

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
