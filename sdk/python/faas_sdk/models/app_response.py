from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_response_runtime import AppResponseRuntime, check_app_response_runtime
from ..models.app_response_type import AppResponseType, check_app_response_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_manifest import AppManifest


T = TypeVar("T", bound="AppResponse")


@_attrs_define
class AppResponse:
    """An app: slug, type, runtime (for functions), RAM/cpu/idle-timeout config, current state, last-deploy pointer, per-
    app outbound CIDR allowlist (ADR-031 + ADR-032), and reactive scale-up trigger targets (issue #169 / #172).

    """

    id: str
    slug: str
    type_: AppResponseType
    ram_mb: int
    max_concurrency: int
    min_instances: int
    status: str
    url: str
    manifest: AppManifest
    """ App manifest: environment variables, build commands, working directory, healthcheck, user, and Dockerfile-
    as-source flag (§ux 6.3). """
    autoscale_target_rps: int
    """ Per-instance RPS target for the reactive scale-up trigger. 0 = disabled. Hobby/Pro/Scale only. When measured
    per-instance RPS exceeds this value, schedd admits another instance (up to max_concurrency). See ADR-037. """
    autoscale_target_cpu_pct: int
    """ Per-instance CPU% target (1..100) for the reactive scale-up trigger. 0 = disabled. Pro/Scale only. When
    measured per-instance CPU% exceeds this value, schedd admits another instance (up to max_concurrency). See
    ADR-037. """
    runtime: AppResponseRuntime | Unset = UNSET
    """ Runtime for `type: function` apps. Omit for `type: app` (the default). """
    idle_timeout_s: int | None | Unset = UNSET
    egress_allowlist: list[str] | Unset = UNSET
    """ Per-app outbound CIDR allowlist (ADR-031 + ADR-032). Each entry is a CIDR string — v4 (`1.2.3.0/24`) or v6
    (`2001:db8::/32`). v4-mapped v6 form (`::ffff:1.2.3.0/120`) is silently canonicalised to its v4 form at write
    time. Empty array means no allowlist rule; the per-netns chain's default-accept policy applies. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        slug = self.slug

        type_: str = self.type_

        ram_mb = self.ram_mb

        max_concurrency = self.max_concurrency

        min_instances = self.min_instances

        status = self.status

        url = self.url

        manifest = self.manifest.to_dict()

        autoscale_target_rps = self.autoscale_target_rps

        autoscale_target_cpu_pct = self.autoscale_target_cpu_pct

        runtime: str | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime

        idle_timeout_s: int | None | Unset
        if isinstance(self.idle_timeout_s, Unset):
            idle_timeout_s = UNSET
        else:
            idle_timeout_s = self.idle_timeout_s

        egress_allowlist: list[str] | Unset = UNSET
        if not isinstance(self.egress_allowlist, Unset):
            egress_allowlist = self.egress_allowlist

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "slug": slug,
                "type": type_,
                "ram_mb": ram_mb,
                "max_concurrency": max_concurrency,
                "min_instances": min_instances,
                "status": status,
                "url": url,
                "manifest": manifest,
                "autoscale_target_rps": autoscale_target_rps,
                "autoscale_target_cpu_pct": autoscale_target_cpu_pct,
            }
        )
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if idle_timeout_s is not UNSET:
            field_dict["idle_timeout_s"] = idle_timeout_s
        if egress_allowlist is not UNSET:
            field_dict["egress_allowlist"] = egress_allowlist

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_manifest import AppManifest

        d = dict(src_dict)
        id = d.pop("id")

        slug = d.pop("slug")

        type_ = check_app_response_type(d.pop("type"))

        ram_mb = d.pop("ram_mb")

        max_concurrency = d.pop("max_concurrency")

        min_instances = d.pop("min_instances")

        status = d.pop("status")

        url = d.pop("url")

        manifest = AppManifest.from_dict(d.pop("manifest"))

        autoscale_target_rps = d.pop("autoscale_target_rps")

        autoscale_target_cpu_pct = d.pop("autoscale_target_cpu_pct")

        _runtime = d.pop("runtime", UNSET)
        runtime: AppResponseRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_app_response_runtime(_runtime)

        def _parse_idle_timeout_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        idle_timeout_s = _parse_idle_timeout_s(d.pop("idle_timeout_s", UNSET))

        egress_allowlist = cast(list[str], d.pop("egress_allowlist", UNSET))

        app_response = cls(
            id=id,
            slug=slug,
            type_=type_,
            ram_mb=ram_mb,
            max_concurrency=max_concurrency,
            min_instances=min_instances,
            status=status,
            url=url,
            manifest=manifest,
            autoscale_target_rps=autoscale_target_rps,
            autoscale_target_cpu_pct=autoscale_target_cpu_pct,
            runtime=runtime,
            idle_timeout_s=idle_timeout_s,
            egress_allowlist=egress_allowlist,
        )

        app_response.additional_properties = d
        return app_response

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
