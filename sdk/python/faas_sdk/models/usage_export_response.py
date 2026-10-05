from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UsageExportResponse")


@_attrs_define
class UsageExportResponse:
    """One usage record: app id, GB-hours consumed, started/finished timestamps for the export window. cpu_usec is the
    per-(app, month) cumulative host cgroup CPU-µs (issue #279 / PR-B). Informational only — billing is on mb_seconds.

    """

    app_id: str
    month: str
    mb_seconds: int
    requests: int
    cpu_usec: int | Unset = UNSET
    """Cumulative host cgroup CPU-µs consumed by the app in the export window (informational; not billed). issue
    #279 / PR-B."""
    tx_bytes: int | Unset = UNSET
    """Per-(app, month) HTTP response bytes (informational; not billed). ADR-046. The gateway-side producer lands
    in PR-2; until then this field stays 0."""
    net_tx_bytes: int | Unset = UNSET
    """Per-(app, month) byte delta on root-side vethHost.rx_bytes (informational; not billed). ADR-046. Sourced
    from vmmd netstats.Cache via schedd ListInstanceStats. Includes Ethernet framing — same kernel counter the per-
    plan tc tbf qdisc reads."""
    net_rx_bytes: int | Unset = UNSET
    """Per-(app, month) byte delta on root-side vethHost.tx_bytes (root→guest = ingress; informational; not
    billed). ADR-048. Mirror of `net_tx_bytes` for the inbound direction."""
    cold_boots: int | Unset = UNSET
    """Per-(app, month) count of WAKE_RESTORE→WAKE_COLD_BOOT transitions observed (informational; not billed).
    ADR-048."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        month = self.month

        mb_seconds = self.mb_seconds

        requests = self.requests

        cpu_usec = self.cpu_usec

        tx_bytes = self.tx_bytes

        net_tx_bytes = self.net_tx_bytes

        net_rx_bytes = self.net_rx_bytes

        cold_boots = self.cold_boots

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "month": month,
                "mb_seconds": mb_seconds,
                "requests": requests,
            }
        )
        if cpu_usec is not UNSET:
            field_dict["cpu_usec"] = cpu_usec
        if tx_bytes is not UNSET:
            field_dict["tx_bytes"] = tx_bytes
        if net_tx_bytes is not UNSET:
            field_dict["net_tx_bytes"] = net_tx_bytes
        if net_rx_bytes is not UNSET:
            field_dict["net_rx_bytes"] = net_rx_bytes
        if cold_boots is not UNSET:
            field_dict["cold_boots"] = cold_boots

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        month = d.pop("month")

        mb_seconds = d.pop("mb_seconds")

        requests = d.pop("requests")

        cpu_usec = d.pop("cpu_usec", UNSET)

        tx_bytes = d.pop("tx_bytes", UNSET)

        net_tx_bytes = d.pop("net_tx_bytes", UNSET)

        net_rx_bytes = d.pop("net_rx_bytes", UNSET)

        cold_boots = d.pop("cold_boots", UNSET)

        usage_export_response = cls(
            app_id=app_id,
            month=month,
            mb_seconds=mb_seconds,
            requests=requests,
            cpu_usec=cpu_usec,
            tx_bytes=tx_bytes,
            net_tx_bytes=net_tx_bytes,
            net_rx_bytes=net_rx_bytes,
            cold_boots=cold_boots,
        )

        usage_export_response.additional_properties = d
        return usage_export_response

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
