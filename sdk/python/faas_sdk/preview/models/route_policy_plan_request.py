from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_requirements_config import RouteRequirementsConfig


T = TypeVar("T", bound="RoutePolicyPlanRequest")


@_attrs_define
class RoutePolicyPlanRequest:
    """Supply exactly one source: inline requirements or saved=true. Saved mode reads current app intent in the same
    snapshot as policy and capture; deployment_id is required. expected_revision optionally pins saved planning.
    Explicit burst choice is required for creating throttles without a selected policy.

    """

    saved: bool | Unset = False
    """Use current saved app requirements instead of inline requirements."""
    expected_revision: int | Unset = UNSET
    """Saved mode only; optional for planning and mandatory for applying the reviewed saved plan."""
    requirements: RouteRequirementsConfig | Unset = UNSET
    """Version 1 requires 1..500 concrete routes. Version 2 assigns every captured operation to groups, concrete
    routes, or public exceptions; overlapping groups are conjunctive."""
    deployment_id: UUID | Unset = UNSET
    """Required for saved or inline version 2 requirements; captured deployment must belong to the app."""
    throttle_burst: int | Unset = UNSET
    consolidate_budgets: bool | Unset = False
    """Version 2 requirements only. Propose compatible budgets within declared group prefixes; explicitly permits
    uncaptured and future path scope. Throttles remain separate."""

    def to_dict(self) -> dict[str, Any]:
        saved = self.saved

        expected_revision = self.expected_revision

        requirements: dict[str, Any] | Unset = UNSET
        if not isinstance(self.requirements, Unset):
            requirements = self.requirements.to_dict()

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        throttle_burst = self.throttle_burst

        consolidate_budgets = self.consolidate_budgets

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if saved is not UNSET:
            field_dict["saved"] = saved
        if expected_revision is not UNSET:
            field_dict["expected_revision"] = expected_revision
        if requirements is not UNSET:
            field_dict["requirements"] = requirements
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if throttle_burst is not UNSET:
            field_dict["throttle_burst"] = throttle_burst
        if consolidate_budgets is not UNSET:
            field_dict["consolidate_budgets"] = consolidate_budgets

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_requirements_config import RouteRequirementsConfig

        d = dict(src_dict)
        saved = d.pop("saved", UNSET)

        expected_revision = d.pop("expected_revision", UNSET)

        _requirements = d.pop("requirements", UNSET)
        requirements: RouteRequirementsConfig | Unset
        if isinstance(_requirements, Unset):
            requirements = UNSET
        else:
            requirements = RouteRequirementsConfig.from_dict(_requirements)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        throttle_burst = d.pop("throttle_burst", UNSET)

        consolidate_budgets = d.pop("consolidate_budgets", UNSET)

        route_policy_plan_request = cls(
            saved=saved,
            expected_revision=expected_revision,
            requirements=requirements,
            deployment_id=deployment_id,
            throttle_burst=throttle_burst,
            consolidate_budgets=consolidate_budgets,
        )

        return route_policy_plan_request
