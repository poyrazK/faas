from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_policy_apply_response_gateway_state import (
    RoutePolicyApplyResponseGatewayState,
    check_route_policy_apply_response_gateway_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_policy_receipt import RoutePolicyReceipt


T = TypeVar("T", bound="RoutePolicyApplyResponse")


@_attrs_define
class RoutePolicyApplyResponse:
    """Successful committed receipt with retry replay information and separate gateway convergence metadata."""

    receipt: RoutePolicyReceipt
    """Durable historical transaction outcome, verified configuration, and actual rule identifiers."""
    replayed: bool
    gateway_state: RoutePolicyApplyResponseGatewayState
    gateway_generation: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        receipt = self.receipt.to_dict()

        replayed = self.replayed

        gateway_state: str = self.gateway_state

        gateway_generation = self.gateway_generation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "receipt": receipt,
                "replayed": replayed,
                "gateway_state": gateway_state,
            }
        )
        if gateway_generation is not UNSET:
            field_dict["gateway_generation"] = gateway_generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_policy_receipt import RoutePolicyReceipt

        d = dict(src_dict)
        receipt = RoutePolicyReceipt.from_dict(d.pop("receipt"))

        replayed = d.pop("replayed")

        gateway_state = check_route_policy_apply_response_gateway_state(d.pop("gateway_state"))

        gateway_generation = d.pop("gateway_generation", UNSET)

        route_policy_apply_response = cls(
            receipt=receipt,
            replayed=replayed,
            gateway_state=gateway_state,
            gateway_generation=gateway_generation,
        )

        route_policy_apply_response.additional_properties = d
        return route_policy_apply_response

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
