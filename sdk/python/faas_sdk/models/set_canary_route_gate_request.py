from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.set_canary_route_gate_request_mode import (
    SetCanaryRouteGateRequestMode,
    check_set_canary_route_gate_request_mode,
)

T = TypeVar("T", bound="SetCanaryRouteGateRequest")


@_attrs_define
class SetCanaryRouteGateRequest:
    """Route requirements gate mode to save after comparing the current gate revision."""

    mode: SetCanaryRouteGateRequestMode
    expected_revision: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        expected_revision = self.expected_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "expected_revision": expected_revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_set_canary_route_gate_request_mode(d.pop("mode"))

        expected_revision = d.pop("expected_revision")

        set_canary_route_gate_request = cls(
            mode=mode,
            expected_revision=expected_revision,
        )

        set_canary_route_gate_request.additional_properties = d
        return set_canary_route_gate_request

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
