from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DailyUsageResponse")


@_attrs_define
class DailyUsageResponse:
    """Per-app rollup for one calendar day (informational; not billed). Mirrors the `usage_daily` table populated by the
    meterd rollup loop (ADR-048 §5, migration 00067).

    """

    app_id: str
    day: str
    mb_seconds: int
    """Cumulative mb_seconds for the day (informational; not billed)."""
    requests: int
    """Cumulative request count for the day (informational; not billed)."""
    cpu_usec: int
    """Cumulative host cgroup CPU-µs for the day (informational; not billed). ADR-046 / #279."""
    tx_bytes: int
    """HTTP response bytes for the day (informational; not billed). ADR-046."""
    net_tx_bytes: int
    """Cumulative net_tx_bytes for the day (informational; not billed). ADR-046."""
    net_rx_bytes: int
    """Cumulative ingress bytes for the day (informational; not billed). ADR-048."""
    cold_boots: int
    """Per-day WAKE_RESTORE→WAKE_COLD_BOOT transition count (informational; not billed). ADR-048."""
    builder_seconds: int
    """Builder VM seconds burned by this app on this day (informational; not billed). ADR-048."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        day = self.day

        mb_seconds = self.mb_seconds

        requests = self.requests

        cpu_usec = self.cpu_usec

        tx_bytes = self.tx_bytes

        net_tx_bytes = self.net_tx_bytes

        net_rx_bytes = self.net_rx_bytes

        cold_boots = self.cold_boots

        builder_seconds = self.builder_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "day": day,
                "mb_seconds": mb_seconds,
                "requests": requests,
                "cpu_usec": cpu_usec,
                "tx_bytes": tx_bytes,
                "net_tx_bytes": net_tx_bytes,
                "net_rx_bytes": net_rx_bytes,
                "cold_boots": cold_boots,
                "builder_seconds": builder_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        day = d.pop("day")

        mb_seconds = d.pop("mb_seconds")

        requests = d.pop("requests")

        cpu_usec = d.pop("cpu_usec")

        tx_bytes = d.pop("tx_bytes")

        net_tx_bytes = d.pop("net_tx_bytes")

        net_rx_bytes = d.pop("net_rx_bytes")

        cold_boots = d.pop("cold_boots")

        builder_seconds = d.pop("builder_seconds")

        daily_usage_response = cls(
            app_id=app_id,
            day=day,
            mb_seconds=mb_seconds,
            requests=requests,
            cpu_usec=cpu_usec,
            tx_bytes=tx_bytes,
            net_tx_bytes=net_tx_bytes,
            net_rx_bytes=net_rx_bytes,
            cold_boots=cold_boots,
            builder_seconds=builder_seconds,
        )

        daily_usage_response.additional_properties = d
        return daily_usage_response

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
