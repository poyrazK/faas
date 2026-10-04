from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.application_standard_effective_sources import ApplicationStandardEffectiveSources
    from ..models.application_standard_settings import ApplicationStandardSettings
    from ..models.application_standard_violation import ApplicationStandardViolation


T = TypeVar("T", bound="ApplicationStandardEffective")


@_attrs_define
class ApplicationStandardEffective:
    values: ApplicationStandardSettings
    """Logical control values; resource references contain UUIDs, never credentials."""
    sources: ApplicationStandardEffectiveSources
    violations: list[ApplicationStandardViolation]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        values = self.values.to_dict()

        sources = self.sources.to_dict()

        violations = []
        for violations_item_data in self.violations:
            violations_item = violations_item_data.to_dict()
            violations.append(violations_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "values": values,
                "sources": sources,
                "violations": violations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_effective_sources import ApplicationStandardEffectiveSources
        from ..models.application_standard_settings import ApplicationStandardSettings
        from ..models.application_standard_violation import ApplicationStandardViolation

        d = dict(src_dict)
        values = ApplicationStandardSettings.from_dict(d.pop("values"))

        sources = ApplicationStandardEffectiveSources.from_dict(d.pop("sources"))

        violations = []
        _violations = d.pop("violations")
        for violations_item_data in _violations:
            violations_item = ApplicationStandardViolation.from_dict(violations_item_data)

            violations.append(violations_item)

        application_standard_effective = cls(
            values=values,
            sources=sources,
            violations=violations,
        )

        application_standard_effective.additional_properties = d
        return application_standard_effective

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
