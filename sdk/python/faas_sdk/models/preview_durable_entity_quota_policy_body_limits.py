from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PreviewDurableEntityQuotaPolicyBodyLimits")


@_attrs_define
class PreviewDurableEntityQuotaPolicyBodyLimits:
    """Preview durable entity quota policy — requestBody / content / application/json / schema / properties / limits:
    Proposed tenant admission caps; all fields are required.

    """

    connections: int
    """Preview durable entity quota policy — requestBody / content / application/json / schema / properties /
    limits / properties / connections: Approved connections cap; server validates bounds and monotonic increases.
   """
    grants_per_minute: int
    """Preview durable entity quota policy — requestBody / content / application/json / schema / properties /
    limits / properties / grants per minute: Approved grants per minute cap; server validates bounds and monotonic
    increases."""
    invocations_per_minute: int
    """Preview durable entity quota policy — requestBody / content / application/json / schema / properties /
    limits / properties / invocations per minute: Approved invocations per minute cap; server validates bounds and
    monotonic increases."""
    retained_bytes: int
    """Preview durable entity quota policy — requestBody / content / application/json / schema / properties /
    limits / properties / retained bytes: Approved retained bytes cap; server validates bounds and monotonic
    increases."""
    entity_bytes: int
    """Preview durable entity quota policy — requestBody / content / application/json / schema / properties /
    limits / properties / entity bytes: Approved entity bytes cap; server validates bounds and monotonic increases.
   """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        connections = self.connections

        grants_per_minute = self.grants_per_minute

        invocations_per_minute = self.invocations_per_minute

        retained_bytes = self.retained_bytes

        entity_bytes = self.entity_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "connections": connections,
                "grants_per_minute": grants_per_minute,
                "invocations_per_minute": invocations_per_minute,
                "retained_bytes": retained_bytes,
                "entity_bytes": entity_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        connections = d.pop("connections")

        grants_per_minute = d.pop("grants_per_minute")

        invocations_per_minute = d.pop("invocations_per_minute")

        retained_bytes = d.pop("retained_bytes")

        entity_bytes = d.pop("entity_bytes")

        preview_durable_entity_quota_policy_body_limits = cls(
            connections=connections,
            grants_per_minute=grants_per_minute,
            invocations_per_minute=invocations_per_minute,
            retained_bytes=retained_bytes,
            entity_bytes=entity_bytes,
        )

        preview_durable_entity_quota_policy_body_limits.additional_properties = d
        return preview_durable_entity_quota_policy_body_limits

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
