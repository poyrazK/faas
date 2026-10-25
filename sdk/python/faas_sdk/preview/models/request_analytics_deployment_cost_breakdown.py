from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.request_analytics_deployment_cost import RequestAnalyticsDeploymentCost


T = TypeVar("T", bound="RequestAnalyticsDeploymentCostBreakdown")


@_attrs_define
class RequestAnalyticsDeploymentCostBreakdown:
    """Bounded deployment allocation of the app's estimated raw compute value for the analytics window. It is not an
    invoice amount.

    """

    estimated_millicents: int
    allocated_millicents: int
    unallocated_millicents: int
    other_millicents: int
    """Value allocated to deployments outside the top-N list."""
    other_requests: int
    other_request_share_pct: float
    request_count: int
    deployments: list[RequestAnalyticsDeploymentCost]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        estimated_millicents = self.estimated_millicents

        allocated_millicents = self.allocated_millicents

        unallocated_millicents = self.unallocated_millicents

        other_millicents = self.other_millicents

        other_requests = self.other_requests

        other_request_share_pct = self.other_request_share_pct

        request_count = self.request_count

        deployments = []
        for deployments_item_data in self.deployments:
            deployments_item = deployments_item_data.to_dict()
            deployments.append(deployments_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "estimated_millicents": estimated_millicents,
                "allocated_millicents": allocated_millicents,
                "unallocated_millicents": unallocated_millicents,
                "other_millicents": other_millicents,
                "other_requests": other_requests,
                "other_request_share_pct": other_request_share_pct,
                "request_count": request_count,
                "deployments": deployments,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.request_analytics_deployment_cost import RequestAnalyticsDeploymentCost

        d = dict(src_dict)
        estimated_millicents = d.pop("estimated_millicents")

        allocated_millicents = d.pop("allocated_millicents")

        unallocated_millicents = d.pop("unallocated_millicents")

        other_millicents = d.pop("other_millicents")

        other_requests = d.pop("other_requests")

        other_request_share_pct = d.pop("other_request_share_pct")

        request_count = d.pop("request_count")

        deployments = []
        _deployments = d.pop("deployments")
        for deployments_item_data in _deployments:
            deployments_item = RequestAnalyticsDeploymentCost.from_dict(deployments_item_data)

            deployments.append(deployments_item)

        request_analytics_deployment_cost_breakdown = cls(
            estimated_millicents=estimated_millicents,
            allocated_millicents=allocated_millicents,
            unallocated_millicents=unallocated_millicents,
            other_millicents=other_millicents,
            other_requests=other_requests,
            other_request_share_pct=other_request_share_pct,
            request_count=request_count,
            deployments=deployments,
        )

        request_analytics_deployment_cost_breakdown.additional_properties = d
        return request_analytics_deployment_cost_breakdown

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
