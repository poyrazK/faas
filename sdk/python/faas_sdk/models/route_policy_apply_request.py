from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_requirements_config import RouteRequirementsConfig


T = TypeVar("T", bound="RoutePolicyApplyRequest")


@_attrs_define
class RoutePolicyApplyRequest:
    """Supply exactly one source: inline requirements or saved=true. Saved apply requires expected_revision from the
    reviewed plan and rejects any saved intent revision change. The reviewed fingerprint binds its content hash,
    capture, options and configuration; patch bodies are recomputed under transaction locks. Identical committed retries
    return the original receipt even after saved intent changes.

    """

    expected_plan_sha256: str
    confirm: bool
    saved: bool | Unset = False
    """Apply using the saved app intent bound by the reviewed plan rather than an inline requirements document."""
    expected_revision: int | Unset = UNSET
    """Required saved intent revision from the reviewed plan when saved=true; a changed revision rejects the apply."""
    requirements: RouteRequirementsConfig | Unset = UNSET
    """Version 1 requires 1..500 concrete routes. Version 2 assigns every captured operation to groups, concrete
    routes, or public exceptions; overlapping groups are conjunctive."""
    deployment_id: UUID | Unset = UNSET
    """App-owned captured deployment required for saved or version 2 apply; its contract must match the reviewed
    plan."""
    throttle_burst: int | Unset = UNSET
    consolidate_budgets: bool | Unset = False
    """Must match the reviewed group plan; synthesis is recomputed under transaction locks."""

    def to_dict(self) -> dict[str, Any]:
        expected_plan_sha256 = self.expected_plan_sha256

        confirm = self.confirm

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

        field_dict.update(
            {
                "expected_plan_sha256": expected_plan_sha256,
                "confirm": confirm,
            }
        )
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
        expected_plan_sha256 = d.pop("expected_plan_sha256")

        confirm = d.pop("confirm")

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

        route_policy_apply_request = cls(
            expected_plan_sha256=expected_plan_sha256,
            confirm=confirm,
            saved=saved,
            expected_revision=expected_revision,
            requirements=requirements,
            deployment_id=deployment_id,
            throttle_burst=throttle_burst,
            consolidate_budgets=consolidate_budgets,
        )

        return route_policy_apply_request
