from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.managed_postgres_usage_import_reading_meter import (
    ManagedPostgresUsageImportReadingMeter,
    check_managed_postgres_usage_import_reading_meter,
)

T = TypeVar("T", bound="ManagedPostgresUsageImportReading")


@_attrs_define
class ManagedPostgresUsageImportReading:
    meter: ManagedPostgresUsageImportReadingMeter
    quantity: int

    def to_dict(self) -> dict[str, Any]:
        meter: str = self.meter

        quantity = self.quantity

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "meter": meter,
                "quantity": quantity,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        meter = check_managed_postgres_usage_import_reading_meter(d.pop("meter"))

        quantity = d.pop("quantity")

        managed_postgres_usage_import_reading = cls(
            meter=meter,
            quantity=quantity,
        )

        return managed_postgres_usage_import_reading
