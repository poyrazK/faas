from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UsageResponse")


@_attrs_define
class UsageResponse:
    """Per-app usage for one month: GB-hours consumed, request count, and an informational CPU-µs field (issue #279 /
    PR-B). The CPU dimension is observable but not yet billed.

    """

    app_id: str
    mb_seconds: int
    requests: int
    included_gb_hours: int
    cpu_usec: int | Unset = UNSET
    """Cumulative host cgroup CPU-µs (informational; not billed). issue #279 / PR-B."""
    tx_bytes: int | Unset = UNSET
    """Per-app monthly HTTP response bytes the gateway forwarded (informational; not billed). Diagnostic subset of
    canonical net_tx_bytes and never added to it. ADR-046."""
    net_tx_bytes: int | Unset = UNSET
    """Per-app monthly byte delta on root-side vethHost.rx_bytes. Canonical optional egress-billing source;
    informational while the provider policy is off. ADR-046. Includes Ethernet framing — the same kernel counter
    used by shaping."""
    net_rx_bytes: int | Unset = UNSET
    """Per-app monthly byte delta on root-side vethHost.tx_bytes (root→guest = ingress; informational; not billed).
    ADR-048. Mirror of `net_tx_bytes` for the inbound direction. Same sysfs source —
    `/sys/class/net/<vethHost>/statistics/tx_bytes`."""
    cold_boots: int | Unset = UNSET
    """Per-app monthly count of WAKE_RESTORE→WAKE_COLD_BOOT transitions observed (informational; not billed).
    ADR-048. Source: schedd instancestats.Poller → meterd Sampler."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        mb_seconds = self.mb_seconds

        requests = self.requests

        included_gb_hours = self.included_gb_hours

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
                "mb_seconds": mb_seconds,
                "requests": requests,
                "included_gb_hours": included_gb_hours,
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

        mb_seconds = d.pop("mb_seconds")

        requests = d.pop("requests")

        included_gb_hours = d.pop("included_gb_hours")

        cpu_usec = d.pop("cpu_usec", UNSET)

        tx_bytes = d.pop("tx_bytes", UNSET)

        net_tx_bytes = d.pop("net_tx_bytes", UNSET)

        net_rx_bytes = d.pop("net_rx_bytes", UNSET)

        cold_boots = d.pop("cold_boots", UNSET)

        usage_response = cls(
            app_id=app_id,
            mb_seconds=mb_seconds,
            requests=requests,
            included_gb_hours=included_gb_hours,
            cpu_usec=cpu_usec,
            tx_bytes=tx_bytes,
            net_tx_bytes=net_tx_bytes,
            net_rx_bytes=net_rx_bytes,
            cold_boots=cold_boots,
        )

        usage_response.additional_properties = d
        return usage_response

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
