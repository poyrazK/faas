from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="UpdateDeploymentTrafficRequestExpectedTraffic")


@_attrs_define
class UpdateDeploymentTrafficRequestExpectedTraffic:
    """Optional split the change was planned from: every live release in the deployment's environment with positive
    traffic, keyed by deployment id, mapped to its percent (1-100, summing to 100, at most 100 releases). If the split
    differs at the transaction boundary, the update returns 409 traffic_serving_changed without changing traffic. Cannot
    be combined with expected_serving_deployment_id or expected_review.

    """

    additional_properties: dict[str, int] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        update_deployment_traffic_request_expected_traffic = cls()

        update_deployment_traffic_request_expected_traffic.additional_properties = d
        return update_deployment_traffic_request_expected_traffic

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> int:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: int) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
