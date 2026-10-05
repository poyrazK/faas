from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_binding_probe_policy_method import (
    OutboundBindingProbePolicyMethod,
    check_outbound_binding_probe_policy_method,
)

T = TypeVar("T", bound="OutboundBindingProbePolicy")


@_attrs_define
class OutboundBindingProbePolicy:
    """Explicit provider endpoint declared safe to probe using managed outbound admission. Queries, redirects and
    unsuccessful expected statuses are unsupported.

    """

    method: OutboundBindingProbePolicyMethod
    path: str
    expected_status: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        expected_status = self.expected_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "expected_status": expected_status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = check_outbound_binding_probe_policy_method(d.pop("method"))

        path = d.pop("path")

        expected_status = d.pop("expected_status")

        outbound_binding_probe_policy = cls(
            method=method,
            path=path,
            expected_status=expected_status,
        )

        outbound_binding_probe_policy.additional_properties = d
        return outbound_binding_probe_policy

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
