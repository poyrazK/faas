from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.application_standard_source import ApplicationStandardSource


T = TypeVar("T", bound="ApplicationStandardViolation")


@_attrs_define
class ApplicationStandardViolation:
    """A field-level constraint violation and the inherited source that imposed it."""

    field: str
    code: str
    source: ApplicationStandardSource
    """Inherited assignment, enforcement mode and any applicable exception contributing to one field."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        field = self.field

        code = self.code

        source = self.source.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "field": field,
                "code": code,
                "source": source,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_source import ApplicationStandardSource

        d = dict(src_dict)
        field = d.pop("field")

        code = d.pop("code")

        source = ApplicationStandardSource.from_dict(d.pop("source"))

        application_standard_violation = cls(
            field=field,
            code=code,
            source=source,
        )

        application_standard_violation.additional_properties = d
        return application_standard_violation

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
