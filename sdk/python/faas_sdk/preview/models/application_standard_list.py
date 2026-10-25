from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_version import ApplicationStandardVersion


T = TypeVar("T", bound="ApplicationStandardList")


@_attrs_define
class ApplicationStandardList:
    """The latest candidates ordered by standard slug."""

    standards: list[ApplicationStandardVersion]
    next_page_after: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        standards = []
        for standards_item_data in self.standards:
            standards_item = standards_item_data.to_dict()
            standards.append(standards_item)

        next_page_after = self.next_page_after

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "standards": standards,
            }
        )
        if next_page_after is not UNSET:
            field_dict["next_page_after"] = next_page_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_version import ApplicationStandardVersion

        d = dict(src_dict)
        standards = []
        _standards = d.pop("standards")
        for standards_item_data in _standards:
            standards_item = ApplicationStandardVersion.from_dict(standards_item_data)

            standards.append(standards_item)

        next_page_after = d.pop("next_page_after", UNSET)

        application_standard_list = cls(
            standards=standards,
            next_page_after=next_page_after,
        )

        application_standard_list.additional_properties = d
        return application_standard_list

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
