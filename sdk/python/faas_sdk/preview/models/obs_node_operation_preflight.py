from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsNodeOperationPreflight")


@_attrs_define
class ObsNodeOperationPreflight:
    """Bounded impact summary captured before a provider compute-node lifecycle intent is enqueued."""

    affected_apps: int
    affected_tenants: int
    total_instances: int
    live_instances: int
    live_ram_mb: int
    capacity_change_mb: int
    """Signed admission-capacity delta if the requested lifecycle transition lands; zero for an idempotent request."""
    reversible: bool
    disruption_warning: bool
    """True when a force-drain targets a node that still has live instances."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        affected_apps = self.affected_apps

        affected_tenants = self.affected_tenants

        total_instances = self.total_instances

        live_instances = self.live_instances

        live_ram_mb = self.live_ram_mb

        capacity_change_mb = self.capacity_change_mb

        reversible = self.reversible

        disruption_warning = self.disruption_warning

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "affected_apps": affected_apps,
                "affected_tenants": affected_tenants,
                "total_instances": total_instances,
                "live_instances": live_instances,
                "live_ram_mb": live_ram_mb,
                "capacity_change_mb": capacity_change_mb,
                "reversible": reversible,
                "disruption_warning": disruption_warning,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        affected_apps = d.pop("affected_apps")

        affected_tenants = d.pop("affected_tenants")

        total_instances = d.pop("total_instances")

        live_instances = d.pop("live_instances")

        live_ram_mb = d.pop("live_ram_mb")

        capacity_change_mb = d.pop("capacity_change_mb")

        reversible = d.pop("reversible")

        disruption_warning = d.pop("disruption_warning")

        obs_node_operation_preflight = cls(
            affected_apps=affected_apps,
            affected_tenants=affected_tenants,
            total_instances=total_instances,
            live_instances=live_instances,
            live_ram_mb=live_ram_mb,
            capacity_change_mb=capacity_change_mb,
            reversible=reversible,
            disruption_warning=disruption_warning,
        )

        obs_node_operation_preflight.additional_properties = d
        return obs_node_operation_preflight

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
