from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.route_lifecycle_mapping import RouteLifecycleMapping


T = TypeVar("T", bound="ApproveRouteLifecycleRequest")


@_attrs_define
class ApproveRouteLifecycleRequest:
    expected_gate_revision: int
    expected_requirements_revision: int
    expected_removal_policy_revision: int
    configuration_sha256: str
    """Configuration digest from the server's saved route requirements check."""
    baseline_deployment_id: UUID
    candidate_deployment_id: UUID
    baseline_contract_sha256: str
    """Authoritative doc_sha256 capture metadata; normalized document hashes are not accepted."""
    candidate_contract_sha256: str
    mappings: list[RouteLifecycleMapping]

    def to_dict(self) -> dict[str, Any]:
        expected_gate_revision = self.expected_gate_revision

        expected_requirements_revision = self.expected_requirements_revision

        expected_removal_policy_revision = self.expected_removal_policy_revision

        configuration_sha256 = self.configuration_sha256

        baseline_deployment_id = str(self.baseline_deployment_id)

        candidate_deployment_id = str(self.candidate_deployment_id)

        baseline_contract_sha256 = self.baseline_contract_sha256

        candidate_contract_sha256 = self.candidate_contract_sha256

        mappings = []
        for mappings_item_data in self.mappings:
            mappings_item = mappings_item_data.to_dict()
            mappings.append(mappings_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_gate_revision": expected_gate_revision,
                "expected_requirements_revision": expected_requirements_revision,
                "expected_removal_policy_revision": expected_removal_policy_revision,
                "configuration_sha256": configuration_sha256,
                "baseline_deployment_id": baseline_deployment_id,
                "candidate_deployment_id": candidate_deployment_id,
                "baseline_contract_sha256": baseline_contract_sha256,
                "candidate_contract_sha256": candidate_contract_sha256,
                "mappings": mappings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_lifecycle_mapping import RouteLifecycleMapping

        d = dict(src_dict)
        expected_gate_revision = d.pop("expected_gate_revision")

        expected_requirements_revision = d.pop("expected_requirements_revision")

        expected_removal_policy_revision = d.pop("expected_removal_policy_revision")

        configuration_sha256 = d.pop("configuration_sha256")

        baseline_deployment_id = UUID(d.pop("baseline_deployment_id"))

        candidate_deployment_id = UUID(d.pop("candidate_deployment_id"))

        baseline_contract_sha256 = d.pop("baseline_contract_sha256")

        candidate_contract_sha256 = d.pop("candidate_contract_sha256")

        mappings = []
        _mappings = d.pop("mappings")
        for mappings_item_data in _mappings:
            mappings_item = RouteLifecycleMapping.from_dict(mappings_item_data)

            mappings.append(mappings_item)

        approve_route_lifecycle_request = cls(
            expected_gate_revision=expected_gate_revision,
            expected_requirements_revision=expected_requirements_revision,
            expected_removal_policy_revision=expected_removal_policy_revision,
            configuration_sha256=configuration_sha256,
            baseline_deployment_id=baseline_deployment_id,
            candidate_deployment_id=candidate_deployment_id,
            baseline_contract_sha256=baseline_contract_sha256,
            candidate_contract_sha256=candidate_contract_sha256,
            mappings=mappings,
        )

        return approve_route_lifecycle_request
