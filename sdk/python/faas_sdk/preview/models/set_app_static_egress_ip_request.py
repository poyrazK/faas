from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SetAppStaticEgressIPRequest")


@_attrs_define
class SetAppStaticEgressIPRequest:
    """PUT /v1/apps/{slug}/static-egress-ip body (ADR-119). IP is
    the canonical customer-supplied IPv4 (dotted-quad string).
    The handler validates the family=4 + non-RFC1918 +
    non-link-local + non-multicast shape before the column
    write. Set=false with empty IP means "clear" — the same
    wire body covers the DELETE /keep-IP promotion path
    without a third endpoint.

    """

    ip: str
    """Customer-supplied IPv4 (dotted-quad). Required when
    `set=true`. Empty string when `set=false`.
    """
    set_: bool
    """`true` to pin the IP; `false` to clear the pin."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ip = self.ip

        set_ = self.set_

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ip": ip,
                "set": set_,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ip = d.pop("ip")

        set_ = d.pop("set")

        set_app_static_egress_ip_request = cls(
            ip=ip,
            set_=set_,
        )

        set_app_static_egress_ip_request.additional_properties = d
        return set_app_static_egress_ip_request

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
