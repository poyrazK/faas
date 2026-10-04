from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.managed_postgres_usage_import_reading import ManagedPostgresUsageImportReading


T = TypeVar("T", bound="ManagedPostgresUsageImportWindow")


@_attrs_define
class ManagedPostgresUsageImportWindow:
    from_: datetime.datetime
    to: datetime.datetime
    observed_at: datetime.datetime
    """Actual export observation time, at or after the closed window and no later than the server clock;
    microsecond precision maximum. Import time is never substituted."""
    readings: list[ManagedPostgresUsageImportReading]

    def to_dict(self) -> dict[str, Any]:
        from_ = self.from_.isoformat()

        to = self.to.isoformat()

        observed_at = self.observed_at.isoformat()

        readings = []
        for readings_item_data in self.readings:
            readings_item = readings_item_data.to_dict()
            readings.append(readings_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from": from_,
                "to": to,
                "observed_at": observed_at,
                "readings": readings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_usage_import_reading import ManagedPostgresUsageImportReading

        d = dict(src_dict)
        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        to = datetime.datetime.fromisoformat(d.pop("to"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        readings = []
        _readings = d.pop("readings")
        for readings_item_data in _readings:
            readings_item = ManagedPostgresUsageImportReading.from_dict(readings_item_data)

            readings.append(readings_item)

        managed_postgres_usage_import_window = cls(
            from_=from_,
            to=to,
            observed_at=observed_at,
            readings=readings,
        )

        return managed_postgres_usage_import_window
