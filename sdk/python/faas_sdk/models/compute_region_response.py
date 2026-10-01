from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.compute_region_response_availability import (
    ComputeRegionResponseAvailability,
    check_compute_region_response_availability,
)
from ..models.compute_region_response_observed_health import (
    ComputeRegionResponseObservedHealth,
    check_compute_region_response_observed_health,
)

T = TypeVar("T", bound="ComputeRegionResponse")


@_attrs_define
class ComputeRegionResponse:
    """Durable compute locality with admission policy and observed node state. No capacity reservation is implied."""

    id: str
    display_name: str
    accepting_new_apps: bool
    """Operator policy for new app admission; independent of node health."""
    availability: ComputeRegionResponseAvailability
    """Whether at least one node passes static placement eligibility; does not report spare capacity."""
    observed_health: ComputeRegionResponseObservedHealth
    """Heartbeat freshness among eligible nodes; unknown when no eligible node has reported a heartbeat. Existing
    lifecycle reconciliation remains authoritative for admission."""
    registered_nodes: int
    active_nodes: int
    eligible_nodes: int
    healthy_nodes: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        display_name = self.display_name

        accepting_new_apps = self.accepting_new_apps

        availability: str = self.availability

        observed_health: str = self.observed_health

        registered_nodes = self.registered_nodes

        active_nodes = self.active_nodes

        eligible_nodes = self.eligible_nodes

        healthy_nodes = self.healthy_nodes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "display_name": display_name,
                "accepting_new_apps": accepting_new_apps,
                "availability": availability,
                "observed_health": observed_health,
                "registered_nodes": registered_nodes,
                "active_nodes": active_nodes,
                "eligible_nodes": eligible_nodes,
                "healthy_nodes": healthy_nodes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        display_name = d.pop("display_name")

        accepting_new_apps = d.pop("accepting_new_apps")

        availability = check_compute_region_response_availability(d.pop("availability"))

        observed_health = check_compute_region_response_observed_health(d.pop("observed_health"))

        registered_nodes = d.pop("registered_nodes")

        active_nodes = d.pop("active_nodes")

        eligible_nodes = d.pop("eligible_nodes")

        healthy_nodes = d.pop("healthy_nodes")

        compute_region_response = cls(
            id=id,
            display_name=display_name,
            accepting_new_apps=accepting_new_apps,
            availability=availability,
            observed_health=observed_health,
            registered_nodes=registered_nodes,
            active_nodes=active_nodes,
            eligible_nodes=eligible_nodes,
            healthy_nodes=healthy_nodes,
        )

        compute_region_response.additional_properties = d
        return compute_region_response

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
