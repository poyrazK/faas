from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateDeploymentTrafficRequest")


@_attrs_define
class UpdateDeploymentTrafficRequest:
    """Body for PATCH /v1/deployments/{id}/traffic. Optionally require a particular live sibling to remain the sole 100%
    serving deployment when the update commits.

    """

    traffic_percent: int
    """Per-deployment traffic-split weight. 0 = no traffic (used during rollback). 100 = sole live deployment."""
    expected_serving_deployment_id: str | Unset = UNSET
    """Optional 32-hex or dashed deployment id. If this deployment is no longer the sole live 100% serving sibling
    at the transaction boundary, the update returns 409 traffic_serving_changed without changing traffic."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        traffic_percent = self.traffic_percent

        expected_serving_deployment_id = self.expected_serving_deployment_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "traffic_percent": traffic_percent,
            }
        )
        if expected_serving_deployment_id is not UNSET:
            field_dict["expected_serving_deployment_id"] = expected_serving_deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        traffic_percent = d.pop("traffic_percent")

        expected_serving_deployment_id = d.pop("expected_serving_deployment_id", UNSET)

        update_deployment_traffic_request = cls(
            traffic_percent=traffic_percent,
            expected_serving_deployment_id=expected_serving_deployment_id,
        )

        update_deployment_traffic_request.additional_properties = d
        return update_deployment_traffic_request

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
