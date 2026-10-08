from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_lifecycle_approval_checker_version import (
    RouteLifecycleApprovalCheckerVersion,
    check_route_lifecycle_approval_checker_version,
)
from ..models.route_lifecycle_approval_compatibility import (
    RouteLifecycleApprovalCompatibility,
    check_route_lifecycle_approval_compatibility,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_lifecycle_mapping import RouteLifecycleMapping


T = TypeVar("T", bound="RouteLifecycleApproval")


@_attrs_define
class RouteLifecycleApproval:
    id: UUID
    app_id: UUID
    gate_revision: int
    requirements_revision: int
    removal_policy_revision: int
    configuration_sha256: str
    baseline_deployment_id: UUID
    candidate_deployment_id: UUID
    baseline_contract_sha256: str
    candidate_contract_sha256: str
    mapping_sha256: str
    mappings: list[RouteLifecycleMapping]
    compatibility: RouteLifecycleApprovalCompatibility
    """Server comparison found no supported declared request, response, method, path parameter or security breaks.
    This is not proof of runtime equivalence."""
    checker_version: RouteLifecycleApprovalCheckerVersion
    approved_by: str
    """Authenticated owner/admin account and key or session identity."""
    approved_at: datetime.datetime
    valid_until: datetime.datetime
    invalidated_at: datetime.datetime | Unset = UNSET
    """Permanently invalidated by capture replacement or deletion; old bytes cannot revive the receipt."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        gate_revision = self.gate_revision

        requirements_revision = self.requirements_revision

        removal_policy_revision = self.removal_policy_revision

        configuration_sha256 = self.configuration_sha256

        baseline_deployment_id = str(self.baseline_deployment_id)

        candidate_deployment_id = str(self.candidate_deployment_id)

        baseline_contract_sha256 = self.baseline_contract_sha256

        candidate_contract_sha256 = self.candidate_contract_sha256

        mapping_sha256 = self.mapping_sha256

        mappings = []
        for mappings_item_data in self.mappings:
            mappings_item = mappings_item_data.to_dict()
            mappings.append(mappings_item)

        compatibility: str = self.compatibility

        checker_version: int = self.checker_version

        approved_by = self.approved_by

        approved_at = self.approved_at.isoformat()

        valid_until = self.valid_until.isoformat()

        invalidated_at: str | Unset = UNSET
        if not isinstance(self.invalidated_at, Unset):
            invalidated_at = self.invalidated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "gate_revision": gate_revision,
                "requirements_revision": requirements_revision,
                "removal_policy_revision": removal_policy_revision,
                "configuration_sha256": configuration_sha256,
                "baseline_deployment_id": baseline_deployment_id,
                "candidate_deployment_id": candidate_deployment_id,
                "baseline_contract_sha256": baseline_contract_sha256,
                "candidate_contract_sha256": candidate_contract_sha256,
                "mapping_sha256": mapping_sha256,
                "mappings": mappings,
                "compatibility": compatibility,
                "checker_version": checker_version,
                "approved_by": approved_by,
                "approved_at": approved_at,
                "valid_until": valid_until,
            }
        )
        if invalidated_at is not UNSET:
            field_dict["invalidated_at"] = invalidated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_lifecycle_mapping import RouteLifecycleMapping

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        gate_revision = d.pop("gate_revision")

        requirements_revision = d.pop("requirements_revision")

        removal_policy_revision = d.pop("removal_policy_revision")

        configuration_sha256 = d.pop("configuration_sha256")

        baseline_deployment_id = UUID(d.pop("baseline_deployment_id"))

        candidate_deployment_id = UUID(d.pop("candidate_deployment_id"))

        baseline_contract_sha256 = d.pop("baseline_contract_sha256")

        candidate_contract_sha256 = d.pop("candidate_contract_sha256")

        mapping_sha256 = d.pop("mapping_sha256")

        mappings = []
        _mappings = d.pop("mappings")
        for mappings_item_data in _mappings:
            mappings_item = RouteLifecycleMapping.from_dict(mappings_item_data)

            mappings.append(mappings_item)

        compatibility = check_route_lifecycle_approval_compatibility(d.pop("compatibility"))

        checker_version = check_route_lifecycle_approval_checker_version(d.pop("checker_version"))

        approved_by = d.pop("approved_by")

        approved_at = datetime.datetime.fromisoformat(d.pop("approved_at"))

        valid_until = datetime.datetime.fromisoformat(d.pop("valid_until"))

        _invalidated_at = d.pop("invalidated_at", UNSET)
        invalidated_at: datetime.datetime | Unset
        if isinstance(_invalidated_at, Unset):
            invalidated_at = UNSET
        else:
            invalidated_at = datetime.datetime.fromisoformat(_invalidated_at)

        route_lifecycle_approval = cls(
            id=id,
            app_id=app_id,
            gate_revision=gate_revision,
            requirements_revision=requirements_revision,
            removal_policy_revision=removal_policy_revision,
            configuration_sha256=configuration_sha256,
            baseline_deployment_id=baseline_deployment_id,
            candidate_deployment_id=candidate_deployment_id,
            baseline_contract_sha256=baseline_contract_sha256,
            candidate_contract_sha256=candidate_contract_sha256,
            mapping_sha256=mapping_sha256,
            mappings=mappings,
            compatibility=compatibility,
            checker_version=checker_version,
            approved_by=approved_by,
            approved_at=approved_at,
            valid_until=valid_until,
            invalidated_at=invalidated_at,
        )

        route_lifecycle_approval.additional_properties = d
        return route_lifecycle_approval

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
