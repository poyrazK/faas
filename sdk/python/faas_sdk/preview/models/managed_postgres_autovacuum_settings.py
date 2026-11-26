from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedPostgresAutovacuumSettings")


@_attrs_define
class ManagedPostgresAutovacuumSettings:
    """Active cluster-wide scale factors sampled from PostgreSQL."""

    vacuum_scale_factor: float
    analyze_scale_factor: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        vacuum_scale_factor = self.vacuum_scale_factor

        analyze_scale_factor = self.analyze_scale_factor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "vacuum_scale_factor": vacuum_scale_factor,
                "analyze_scale_factor": analyze_scale_factor,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        vacuum_scale_factor = d.pop("vacuum_scale_factor")

        analyze_scale_factor = d.pop("analyze_scale_factor")

        managed_postgres_autovacuum_settings = cls(
            vacuum_scale_factor=vacuum_scale_factor,
            analyze_scale_factor=analyze_scale_factor,
        )

        managed_postgres_autovacuum_settings.additional_properties = d
        return managed_postgres_autovacuum_settings

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
