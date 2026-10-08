from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.approve_route_removal_request_mappings_item import ApproveRouteRemovalRequestMappingsItem


T = TypeVar("T", bound="ApproveRouteRemovalRequest")


@_attrs_define
class ApproveRouteRemovalRequest:
    expected_policy_revision: int
    baseline_deployment_id: UUID
    candidate_deployment_id: UUID
    baseline_contract_sha256: str
    """Authoritative doc_sha256 capture metadata, not a reserialized document digest."""
    candidate_contract_sha256: str
    mappings: list[ApproveRouteRemovalRequestMappingsItem]
    acknowledge_observed_only: bool
    """Explicit owner acknowledgement that telemetry is sampled and asynchronous and cannot establish absence of
    every client."""

    def to_dict(self) -> dict[str, Any]:
        expected_policy_revision = self.expected_policy_revision

        baseline_deployment_id = str(self.baseline_deployment_id)

        candidate_deployment_id = str(self.candidate_deployment_id)

        baseline_contract_sha256 = self.baseline_contract_sha256

        candidate_contract_sha256 = self.candidate_contract_sha256

        mappings = []
        for mappings_item_data in self.mappings:
            mappings_item = mappings_item_data.to_dict()
            mappings.append(mappings_item)

        acknowledge_observed_only = self.acknowledge_observed_only

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_policy_revision": expected_policy_revision,
                "baseline_deployment_id": baseline_deployment_id,
                "candidate_deployment_id": candidate_deployment_id,
                "baseline_contract_sha256": baseline_contract_sha256,
                "candidate_contract_sha256": candidate_contract_sha256,
                "mappings": mappings,
                "acknowledge_observed_only": acknowledge_observed_only,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.approve_route_removal_request_mappings_item import ApproveRouteRemovalRequestMappingsItem

        d = dict(src_dict)
        expected_policy_revision = d.pop("expected_policy_revision")

        baseline_deployment_id = UUID(d.pop("baseline_deployment_id"))

        candidate_deployment_id = UUID(d.pop("candidate_deployment_id"))

        baseline_contract_sha256 = d.pop("baseline_contract_sha256")

        candidate_contract_sha256 = d.pop("candidate_contract_sha256")

        mappings = []
        _mappings = d.pop("mappings")
        for mappings_item_data in _mappings:
            mappings_item = ApproveRouteRemovalRequestMappingsItem.from_dict(mappings_item_data)

            mappings.append(mappings_item)

        acknowledge_observed_only = d.pop("acknowledge_observed_only")

        approve_route_removal_request = cls(
            expected_policy_revision=expected_policy_revision,
            baseline_deployment_id=baseline_deployment_id,
            candidate_deployment_id=candidate_deployment_id,
            baseline_contract_sha256=baseline_contract_sha256,
            candidate_contract_sha256=candidate_contract_sha256,
            mappings=mappings,
            acknowledge_observed_only=acknowledge_observed_only,
        )

        return approve_route_removal_request
