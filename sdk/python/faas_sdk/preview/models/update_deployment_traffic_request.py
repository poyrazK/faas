from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_release_review import AppReleaseReview
    from ..models.update_deployment_traffic_request_expected_mcp_access_policy import (
        UpdateDeploymentTrafficRequestExpectedMcpAccessPolicy,
    )
    from ..models.update_deployment_traffic_request_expected_traffic import (
        UpdateDeploymentTrafficRequestExpectedTraffic,
    )


T = TypeVar("T", bound="UpdateDeploymentTrafficRequest")


@_attrs_define
class UpdateDeploymentTrafficRequest:
    """Body for PATCH /v1/deployments/{id}/traffic. Optionally require a particular live sibling to remain the sole 100%
    serving deployment when the update commits.

    """

    traffic_percent: int
    """Per-deployment traffic-split weight. 0 = no traffic (used during rollback). 100 = sole live deployment."""
    expected_review: AppReleaseReview | Unset = UNSET
    """Atomic app configuration and serving-deployment identities used to fence release admission."""
    expected_serving_deployment_id: str | Unset = UNSET
    """Optional 32-hex or dashed deployment id. An explicit empty string requires no live sibling with positive
    traffic, for guarded first deployment promotion. If the condition changes at the transaction boundary, the
    update returns 409 traffic_serving_changed without changing traffic."""
    expected_traffic: UpdateDeploymentTrafficRequestExpectedTraffic | Unset = UNSET
    """Optional split the change was planned from: every live release in the deployment's environment with positive
    traffic, keyed by deployment id, mapped to its percent (1-100, summing to 100, at most 100 releases). If the
    split differs at the transaction boundary, the update returns 409 traffic_serving_changed without changing
    traffic. Cannot be combined with expected_serving_deployment_id or expected_review."""
    expected_mcp_access_policy: UpdateDeploymentTrafficRequestExpectedMcpAccessPolicy | Unset = UNSET
    """Optional account-wide stored MCP policy condition, checked in the traffic transaction. Changed overlapping
    JWT rules return 409 mcp_access_policy_changed without changing traffic. When account_revision is supplied, the
    transaction also requires the unchanged account revision and fresh replay from every currently registered
    serving gateway. Gateway evidence is a database snapshot at admission, not runtime authorization or a lease on
    fleet membership."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        traffic_percent = self.traffic_percent

        expected_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_review, Unset):
            expected_review = self.expected_review.to_dict()

        expected_serving_deployment_id = self.expected_serving_deployment_id

        expected_traffic: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_traffic, Unset):
            expected_traffic = self.expected_traffic.to_dict()

        expected_mcp_access_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected_mcp_access_policy, Unset):
            expected_mcp_access_policy = self.expected_mcp_access_policy.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "traffic_percent": traffic_percent,
            }
        )
        if expected_review is not UNSET:
            field_dict["expected_review"] = expected_review
        if expected_serving_deployment_id is not UNSET:
            field_dict["expected_serving_deployment_id"] = expected_serving_deployment_id
        if expected_traffic is not UNSET:
            field_dict["expected_traffic"] = expected_traffic
        if expected_mcp_access_policy is not UNSET:
            field_dict["expected_mcp_access_policy"] = expected_mcp_access_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_release_review import AppReleaseReview
        from ..models.update_deployment_traffic_request_expected_mcp_access_policy import (
            UpdateDeploymentTrafficRequestExpectedMcpAccessPolicy,
        )
        from ..models.update_deployment_traffic_request_expected_traffic import (
            UpdateDeploymentTrafficRequestExpectedTraffic,
        )

        d = dict(src_dict)
        traffic_percent = d.pop("traffic_percent")

        _expected_review = d.pop("expected_review", UNSET)
        expected_review: AppReleaseReview | Unset
        if isinstance(_expected_review, Unset):
            expected_review = UNSET
        else:
            expected_review = AppReleaseReview.from_dict(_expected_review)

        expected_serving_deployment_id = d.pop("expected_serving_deployment_id", UNSET)

        _expected_traffic = d.pop("expected_traffic", UNSET)
        expected_traffic: UpdateDeploymentTrafficRequestExpectedTraffic | Unset
        if isinstance(_expected_traffic, Unset):
            expected_traffic = UNSET
        else:
            expected_traffic = UpdateDeploymentTrafficRequestExpectedTraffic.from_dict(_expected_traffic)

        _expected_mcp_access_policy = d.pop("expected_mcp_access_policy", UNSET)
        expected_mcp_access_policy: UpdateDeploymentTrafficRequestExpectedMcpAccessPolicy | Unset
        if isinstance(_expected_mcp_access_policy, Unset):
            expected_mcp_access_policy = UNSET
        else:
            expected_mcp_access_policy = UpdateDeploymentTrafficRequestExpectedMcpAccessPolicy.from_dict(
                _expected_mcp_access_policy
            )

        update_deployment_traffic_request = cls(
            traffic_percent=traffic_percent,
            expected_review=expected_review,
            expected_serving_deployment_id=expected_serving_deployment_id,
            expected_traffic=expected_traffic,
            expected_mcp_access_policy=expected_mcp_access_policy,
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
