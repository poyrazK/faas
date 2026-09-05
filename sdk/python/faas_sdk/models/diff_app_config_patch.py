from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.diff_app_config_patch_app_protocol import (
    DiffAppConfigPatchAppProtocol,
    check_diff_app_config_patch_app_protocol,
)
from ..models.diff_app_config_patch_cpu_millicores import (
    DiffAppConfigPatchCpuMillicores,
    check_diff_app_config_patch_cpu_millicores,
)
from ..models.diff_app_config_patch_eviction_priority import (
    DiffAppConfigPatchEvictionPriority,
    check_diff_app_config_patch_eviction_priority,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DiffAppConfigPatch")


@_attrs_define
class DiffAppConfigPatch:
    """Per-app scalar patch. Pointer-aware: nil = "don't touch";
    explicit zero / explicit value = "set to this". Matches
    [UpdateAppRequest] semantics but exposes only the fields
    the engine computes against (no ScalingPolicy /
    PublicAuth / OverflowNode).

    """

    ram_mb: int | Unset = UNSET
    cpu_millicores: DiffAppConfigPatchCpuMillicores | Unset = UNSET
    idle_timeout_s: int | Unset = UNSET
    max_concurrency: int | Unset = UNSET
    min_instances: int | Unset = UNSET
    egress_allowlist: list[str] | Unset = UNSET
    autoscale_target_rps: int | Unset = UNSET
    autoscale_target_cpu_pct: int | Unset = UNSET
    streaming_enabled: bool | Unset = UNSET
    websocket_enabled: bool | Unset = UNSET
    require_signed: bool | Unset = UNSET
    warm_snapshot_enabled: bool | Unset = UNSET
    require_authn: bool | Unset = UNSET
    eviction_priority: DiffAppConfigPatchEvictionPriority | Unset = UNSET
    app_protocol: DiffAppConfigPatchAppProtocol | Unset = UNSET
    """Per-app wire-protocol selector (ADR-124). Same closed set + plan gate as UpdateAppRequest.app_protocol.
    Pointer-aware: omitted → no change; non-null → set to this value."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ram_mb = self.ram_mb

        cpu_millicores: int | Unset = UNSET
        if not isinstance(self.cpu_millicores, Unset):
            cpu_millicores = self.cpu_millicores

        idle_timeout_s = self.idle_timeout_s

        max_concurrency = self.max_concurrency

        min_instances = self.min_instances

        egress_allowlist: list[str] | Unset = UNSET
        if not isinstance(self.egress_allowlist, Unset):
            egress_allowlist = self.egress_allowlist

        autoscale_target_rps = self.autoscale_target_rps

        autoscale_target_cpu_pct = self.autoscale_target_cpu_pct

        streaming_enabled = self.streaming_enabled

        websocket_enabled = self.websocket_enabled

        require_signed = self.require_signed

        warm_snapshot_enabled = self.warm_snapshot_enabled

        require_authn = self.require_authn

        eviction_priority: str | Unset = UNSET
        if not isinstance(self.eviction_priority, Unset):
            eviction_priority = self.eviction_priority

        app_protocol: str | Unset = UNSET
        if not isinstance(self.app_protocol, Unset):
            app_protocol = self.app_protocol

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if ram_mb is not UNSET:
            field_dict["ram_mb"] = ram_mb
        if cpu_millicores is not UNSET:
            field_dict["cpu_millicores"] = cpu_millicores
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
        if streaming_enabled is not UNSET:
            field_dict["streaming_enabled"] = streaming_enabled
        if websocket_enabled is not UNSET:
            field_dict["websocket_enabled"] = websocket_enabled
        if require_signed is not UNSET:
            field_dict["require_signed"] = require_signed
        if warm_snapshot_enabled is not UNSET:
            field_dict["warm_snapshot_enabled"] = warm_snapshot_enabled
        if require_authn is not UNSET:
            field_dict["require_authn"] = require_authn
        if eviction_priority is not UNSET:
            field_dict["eviction_priority"] = eviction_priority
        if app_protocol is not UNSET:
            field_dict["app_protocol"] = app_protocol

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ram_mb = d.pop("ram_mb", UNSET)

        _cpu_millicores = d.pop("cpu_millicores", UNSET)
        cpu_millicores: DiffAppConfigPatchCpuMillicores | Unset
        if isinstance(_cpu_millicores, Unset):
            cpu_millicores = UNSET
        else:
            cpu_millicores = check_diff_app_config_patch_cpu_millicores(_cpu_millicores)

        idle_timeout_s = d.pop("idle_timeout_s", UNSET)

        max_concurrency = d.pop("max_concurrency", UNSET)

        min_instances = d.pop("min_instances", UNSET)

        egress_allowlist = cast(list[str], d.pop("egress_allowlist", UNSET))

        autoscale_target_rps = d.pop("autoscale_target_rps", UNSET)

        autoscale_target_cpu_pct = d.pop("autoscale_target_cpu_pct", UNSET)

        streaming_enabled = d.pop("streaming_enabled", UNSET)

        websocket_enabled = d.pop("websocket_enabled", UNSET)

        require_signed = d.pop("require_signed", UNSET)

        warm_snapshot_enabled = d.pop("warm_snapshot_enabled", UNSET)

        require_authn = d.pop("require_authn", UNSET)

        _eviction_priority = d.pop("eviction_priority", UNSET)
        eviction_priority: DiffAppConfigPatchEvictionPriority | Unset
        if isinstance(_eviction_priority, Unset):
            eviction_priority = UNSET
        else:
            eviction_priority = check_diff_app_config_patch_eviction_priority(_eviction_priority)

        _app_protocol = d.pop("app_protocol", UNSET)
        app_protocol: DiffAppConfigPatchAppProtocol | Unset
        if isinstance(_app_protocol, Unset):
            app_protocol = UNSET
        else:
            app_protocol = check_diff_app_config_patch_app_protocol(_app_protocol)

        diff_app_config_patch = cls(
            ram_mb=ram_mb,
            cpu_millicores=cpu_millicores,
            idle_timeout_s=idle_timeout_s,
            max_concurrency=max_concurrency,
            min_instances=min_instances,
            egress_allowlist=egress_allowlist,
            autoscale_target_rps=autoscale_target_rps,
            autoscale_target_cpu_pct=autoscale_target_cpu_pct,
            streaming_enabled=streaming_enabled,
            websocket_enabled=websocket_enabled,
            require_signed=require_signed,
            warm_snapshot_enabled=warm_snapshot_enabled,
            require_authn=require_authn,
            eviction_priority=eviction_priority,
            app_protocol=app_protocol,
        )

        diff_app_config_patch.additional_properties = d
        return diff_app_config_patch

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
