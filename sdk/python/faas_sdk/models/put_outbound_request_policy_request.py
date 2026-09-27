from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_request_policy import OutboundRequestPolicy


T = TypeVar("T", bound="PutOutboundRequestPolicyRequest")


@_attrs_define
class PutOutboundRequestPolicyRequest:
    """Replace all admission policy values for a customer-owned integration."""

    request_policy: OutboundRequestPolicy
    """Effective customer-selected per-integration policy, bounded by the account plan."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_policy = self.request_policy.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_policy": request_policy,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_request_policy import OutboundRequestPolicy

        d = dict(src_dict)
        request_policy = OutboundRequestPolicy.from_dict(d.pop("request_policy"))

        put_outbound_request_policy_request = cls(
            request_policy=request_policy,
        )

        put_outbound_request_policy_request.additional_properties = d
        return put_outbound_request_policy_request

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
