from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RequestAnalyticsDeploymentCost")


@_attrs_define
class RequestAnalyticsDeploymentCost:
    """One deployment's estimated share of app raw compute value, allocated by its observed request share."""

    deployment_id: str
    """Immutable deployment UUID."""
    requests: int
    request_share_pct: float
    estimated_compute_cost_millicents: int
    commit_sha: str | Unset = UNSET
    deployment_tag: str | Unset = UNSET
    deployment_created_at: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = self.deployment_id

        requests = self.requests

        request_share_pct = self.request_share_pct

        estimated_compute_cost_millicents = self.estimated_compute_cost_millicents

        commit_sha = self.commit_sha

        deployment_tag = self.deployment_tag

        deployment_created_at = self.deployment_created_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "requests": requests,
                "request_share_pct": request_share_pct,
                "estimated_compute_cost_millicents": estimated_compute_cost_millicents,
            }
        )
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if deployment_tag is not UNSET:
            field_dict["deployment_tag"] = deployment_tag
        if deployment_created_at is not UNSET:
            field_dict["deployment_created_at"] = deployment_created_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = d.pop("deployment_id")

        requests = d.pop("requests")

        request_share_pct = d.pop("request_share_pct")

        estimated_compute_cost_millicents = d.pop("estimated_compute_cost_millicents")

        commit_sha = d.pop("commit_sha", UNSET)

        deployment_tag = d.pop("deployment_tag", UNSET)

        deployment_created_at = d.pop("deployment_created_at", UNSET)

        request_analytics_deployment_cost = cls(
            deployment_id=deployment_id,
            requests=requests,
            request_share_pct=request_share_pct,
            estimated_compute_cost_millicents=estimated_compute_cost_millicents,
            commit_sha=commit_sha,
            deployment_tag=deployment_tag,
            deployment_created_at=deployment_created_at,
        )

        request_analytics_deployment_cost.additional_properties = d
        return request_analytics_deployment_cost

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
