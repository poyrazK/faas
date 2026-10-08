from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_removal_approval_coverage import RouteRemovalApprovalCoverage, check_route_removal_approval_coverage

if TYPE_CHECKING:
    from ..models.route_removal_mapping import RouteRemovalMapping


T = TypeVar("T", bound="RouteRemovalApproval")


@_attrs_define
class RouteRemovalApproval:
    id: UUID
    app_id: UUID
    policy_revision: int
    baseline_deployment_id: UUID
    candidate_deployment_id: UUID
    baseline_contract_sha256: str
    candidate_contract_sha256: str
    mapping_sha256: str
    """Digest of server-normalized mapping entries."""
    mappings: list[RouteRemovalMapping]
    approved_by: str
    """Authenticated account and key or session identity."""
    approved_at: datetime.datetime
    valid_until: datetime.datetime
    observation_from: datetime.datetime
    observation_until: datetime.datetime
    coverage: RouteRemovalApprovalCoverage
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        policy_revision = self.policy_revision

        baseline_deployment_id = str(self.baseline_deployment_id)

        candidate_deployment_id = str(self.candidate_deployment_id)

        baseline_contract_sha256 = self.baseline_contract_sha256

        candidate_contract_sha256 = self.candidate_contract_sha256

        mapping_sha256 = self.mapping_sha256

        mappings = []
        for mappings_item_data in self.mappings:
            mappings_item = mappings_item_data.to_dict()
            mappings.append(mappings_item)

        approved_by = self.approved_by

        approved_at = self.approved_at.isoformat()

        valid_until = self.valid_until.isoformat()

        observation_from = self.observation_from.isoformat()

        observation_until = self.observation_until.isoformat()

        coverage: str = self.coverage

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "policy_revision": policy_revision,
                "baseline_deployment_id": baseline_deployment_id,
                "candidate_deployment_id": candidate_deployment_id,
                "baseline_contract_sha256": baseline_contract_sha256,
                "candidate_contract_sha256": candidate_contract_sha256,
                "mapping_sha256": mapping_sha256,
                "mappings": mappings,
                "approved_by": approved_by,
                "approved_at": approved_at,
                "valid_until": valid_until,
                "observation_from": observation_from,
                "observation_until": observation_until,
                "coverage": coverage,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_removal_mapping import RouteRemovalMapping

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        policy_revision = d.pop("policy_revision")

        baseline_deployment_id = UUID(d.pop("baseline_deployment_id"))

        candidate_deployment_id = UUID(d.pop("candidate_deployment_id"))

        baseline_contract_sha256 = d.pop("baseline_contract_sha256")

        candidate_contract_sha256 = d.pop("candidate_contract_sha256")

        mapping_sha256 = d.pop("mapping_sha256")

        mappings = []
        _mappings = d.pop("mappings")
        for mappings_item_data in _mappings:
            mappings_item = RouteRemovalMapping.from_dict(mappings_item_data)

            mappings.append(mappings_item)

        approved_by = d.pop("approved_by")

        approved_at = datetime.datetime.fromisoformat(d.pop("approved_at"))

        valid_until = datetime.datetime.fromisoformat(d.pop("valid_until"))

        observation_from = datetime.datetime.fromisoformat(d.pop("observation_from"))

        observation_until = datetime.datetime.fromisoformat(d.pop("observation_until"))

        coverage = check_route_removal_approval_coverage(d.pop("coverage"))

        route_removal_approval = cls(
            id=id,
            app_id=app_id,
            policy_revision=policy_revision,
            baseline_deployment_id=baseline_deployment_id,
            candidate_deployment_id=candidate_deployment_id,
            baseline_contract_sha256=baseline_contract_sha256,
            candidate_contract_sha256=candidate_contract_sha256,
            mapping_sha256=mapping_sha256,
            mappings=mappings,
            approved_by=approved_by,
            approved_at=approved_at,
            valid_until=valid_until,
            observation_from=observation_from,
            observation_until=observation_until,
            coverage=coverage,
        )

        route_removal_approval.additional_properties = d
        return route_removal_approval

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
