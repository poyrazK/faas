from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateAppRequest")


@_attrs_define
class UpdateAppRequest:
    """Partial update — every field is optional; omitted fields are unchanged."""

    ram_mb: int | None | Unset = UNSET
    idle_timeout_s: int | None | Unset = UNSET
    max_concurrency: int | None | Unset = UNSET
    min_instances: int | None | Unset = UNSET
    egress_allowlist: list[str] | Unset = UNSET
    """ v4 or v6 CIDR allowlist; empty array clears to chain-default-accept. """
    autoscale_target_rps: int | None | Unset = UNSET
    """ Per-instance RPS target for the reactive scale-up trigger. 0 = disable. Hobby/Pro/Scale only. Values < 0 are
    422 invalid_autoscale_target_rps. """
    autoscale_target_cpu_pct: int | None | Unset = UNSET
    """ Per-instance CPU% target (1..100, 0 = disable) for the reactive scale-up trigger. Pro/Scale only. Values
    outside [1, 100] (other than 0) are 422 invalid_autoscale_target_cpu_pct. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ram_mb: int | None | Unset
        if isinstance(self.ram_mb, Unset):
            ram_mb = UNSET
        else:
            ram_mb = self.ram_mb

        idle_timeout_s: int | None | Unset
        if isinstance(self.idle_timeout_s, Unset):
            idle_timeout_s = UNSET
        else:
            idle_timeout_s = self.idle_timeout_s

        max_concurrency: int | None | Unset
        if isinstance(self.max_concurrency, Unset):
            max_concurrency = UNSET
        else:
            max_concurrency = self.max_concurrency

        min_instances: int | None | Unset
        if isinstance(self.min_instances, Unset):
            min_instances = UNSET
        else:
            min_instances = self.min_instances

        egress_allowlist: list[str] | Unset = UNSET
        if not isinstance(self.egress_allowlist, Unset):
            egress_allowlist = self.egress_allowlist

        autoscale_target_rps: int | None | Unset
        if isinstance(self.autoscale_target_rps, Unset):
            autoscale_target_rps = UNSET
        else:
            autoscale_target_rps = self.autoscale_target_rps

        autoscale_target_cpu_pct: int | None | Unset
        if isinstance(self.autoscale_target_cpu_pct, Unset):
            autoscale_target_cpu_pct = UNSET
        else:
            autoscale_target_cpu_pct = self.autoscale_target_cpu_pct

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if idle_timeout_s is not UNSET:
            field_dict["idle_timeout_s"] = idle_timeout_s
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if min_instances is not UNSET:
            field_dict["min_instances"] = min_instances
        if egress_allowlist is not UNSET:
            field_dict["egress_allowlist"] = egress_allowlist
        if autoscale_target_rps is not UNSET:
            field_dict["autoscale_target_rps"] = autoscale_target_rps
        if autoscale_target_cpu_pct is not UNSET:
            field_dict["autoscale_target_cpu_pct"] = autoscale_target_cpu_pct

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_ram_mb(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ram_mb = _parse_ram_mb(d.pop("ram_mb", UNSET))

        def _parse_idle_timeout_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        idle_timeout_s = _parse_idle_timeout_s(d.pop("idle_timeout_s", UNSET))

        def _parse_max_concurrency(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        max_concurrency = _parse_max_concurrency(d.pop("max_concurrency", UNSET))

        def _parse_min_instances(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        min_instances = _parse_min_instances(d.pop("min_instances", UNSET))

        egress_allowlist = cast(list[str], d.pop("egress_allowlist", UNSET))

        def _parse_autoscale_target_rps(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        autoscale_target_rps = _parse_autoscale_target_rps(d.pop("autoscale_target_rps", UNSET))

        def _parse_autoscale_target_cpu_pct(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        autoscale_target_cpu_pct = _parse_autoscale_target_cpu_pct(d.pop("autoscale_target_cpu_pct", UNSET))

        update_app_request = cls(
            ram_mb=ram_mb,
            idle_timeout_s=idle_timeout_s,
            max_concurrency=max_concurrency,
            min_instances=min_instances,
            egress_allowlist=egress_allowlist,
            autoscale_target_rps=autoscale_target_rps,
            autoscale_target_cpu_pct=autoscale_target_cpu_pct,
        )

        update_app_request.additional_properties = d
        return update_app_request

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
