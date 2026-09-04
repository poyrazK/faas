from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ServiceReplicas")


@_attrs_define
class ServiceReplicas:
    """Per-deployment replica scaffold for execution_mode='service' (ADR-137 §Decision 3, M-2 + M-4 workstream E). Replica
    count is bounded by ServiceReplicasMax per plan (Hobby 3, Pro 5, Scale 20). min ≤ desired ≤ max must hold.
    Foundation here; rolling-deploy / rollback / image-digest pinning semantics land in M-4.

    """

    min_: int
    """Minimum desired replicas the Engine keeps alive. 0 = no minimum."""
    max_: int
    """Maximum desired replicas (engine-side cap; service autoscale bounds)."""
    desired: int
    """Desired replica count. Engine wakes replacement instances to maintain this when one fails or is destroyed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        min_ = self.min_

        max_ = self.max_

        desired = self.desired

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "min": min_,
                "max": max_,
                "desired": desired,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        min_ = d.pop("min")

        max_ = d.pop("max")

        desired = d.pop("desired")

        service_replicas = cls(
            min_=min_,
            max_=max_,
            desired=desired,
        )

        service_replicas.additional_properties = d
        return service_replicas

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
