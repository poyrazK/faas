from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.binding_check_report import BindingCheckReport


T = TypeVar("T", bound="CheckedProjectReleaseResponseBindingsChecks")


@_attrs_define
class CheckedProjectReleaseResponseBindingsChecks:
    additional_properties: dict[str, BindingCheckReport] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_check_report import BindingCheckReport

        d = dict(src_dict)
        checked_project_release_response_bindings_checks = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = BindingCheckReport.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        checked_project_release_response_bindings_checks.additional_properties = additional_properties
        return checked_project_release_response_bindings_checks

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> BindingCheckReport:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: BindingCheckReport) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
