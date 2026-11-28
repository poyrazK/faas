from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_binding_inventory import AppBindingInventory
    from ..models.binding_check_finding import BindingCheckFinding
    from ..models.binding_check_report import BindingCheckReport
    from ..models.binding_release_policy import BindingReleasePolicy
    from ..models.binding_verification_plan_step import BindingVerificationPlanStep
    from ..models.binding_verification_review_service_targets import BindingVerificationReviewServiceTargets


T = TypeVar("T", bound="BindingVerificationReview")


@_attrs_define
class BindingVerificationReview:
    """Read-only application binding plan used to review selection and consent before receipt creation."""

    app: str
    deployment_id: UUID
    scope: str
    service_targets: BindingVerificationReviewServiceTargets
    reviewed_at: datetime.datetime
    ready: bool
    review_digest: str
    object_storage_write_required: bool
    queue_publish_required: bool
    inventory: AppBindingInventory
    """Best-effort app binding metadata with explicit completeness and sanitized section issues."""
    policy: BindingReleasePolicy
    """Stored binding verification requirements and revision for an app deployment scope; unconfigured scopes
    default to off."""
    plan: list[BindingVerificationPlanStep]
    blockers: list[BindingCheckFinding]
    observed_check: BindingCheckReport | Unset = UNSET
    """Safe preflight findings for the declared policy at checked_at. Coverage may be complete, partial or none; a
    passed report does not independently establish application readiness or credential use. Optional application
    acknowledgements are version-bound self-attestations."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        deployment_id = str(self.deployment_id)

        scope = self.scope

        service_targets = self.service_targets.to_dict()

        reviewed_at = self.reviewed_at.isoformat()

        ready = self.ready

        review_digest = self.review_digest

        object_storage_write_required = self.object_storage_write_required

        queue_publish_required = self.queue_publish_required

        inventory = self.inventory.to_dict()

        policy = self.policy.to_dict()

        plan = []
        for plan_item_data in self.plan:
            plan_item = plan_item_data.to_dict()
            plan.append(plan_item)

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        observed_check: dict[str, Any] | Unset = UNSET
        if not isinstance(self.observed_check, Unset):
            observed_check = self.observed_check.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "deployment_id": deployment_id,
                "scope": scope,
                "service_targets": service_targets,
                "reviewed_at": reviewed_at,
                "ready": ready,
                "review_digest": review_digest,
                "object_storage_write_required": object_storage_write_required,
                "queue_publish_required": queue_publish_required,
                "inventory": inventory,
                "policy": policy,
                "plan": plan,
                "blockers": blockers,
            }
        )
        if observed_check is not UNSET:
            field_dict["observed_check"] = observed_check

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_binding_inventory import AppBindingInventory
        from ..models.binding_check_finding import BindingCheckFinding
        from ..models.binding_check_report import BindingCheckReport
        from ..models.binding_release_policy import BindingReleasePolicy
        from ..models.binding_verification_plan_step import BindingVerificationPlanStep
        from ..models.binding_verification_review_service_targets import BindingVerificationReviewServiceTargets

        d = dict(src_dict)
        app = d.pop("app")

        deployment_id = UUID(d.pop("deployment_id"))

        scope = d.pop("scope")

        service_targets = BindingVerificationReviewServiceTargets.from_dict(d.pop("service_targets"))

        reviewed_at = datetime.datetime.fromisoformat(d.pop("reviewed_at"))

        ready = d.pop("ready")

        review_digest = d.pop("review_digest")

        object_storage_write_required = d.pop("object_storage_write_required")

        queue_publish_required = d.pop("queue_publish_required")

        inventory = AppBindingInventory.from_dict(d.pop("inventory"))

        policy = BindingReleasePolicy.from_dict(d.pop("policy"))

        plan = []
        _plan = d.pop("plan")
        for plan_item_data in _plan:
            plan_item = BindingVerificationPlanStep.from_dict(plan_item_data)

            plan.append(plan_item)

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = BindingCheckFinding.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        _observed_check = d.pop("observed_check", UNSET)
        observed_check: BindingCheckReport | Unset
        if isinstance(_observed_check, Unset):
            observed_check = UNSET
        else:
            observed_check = BindingCheckReport.from_dict(_observed_check)

        binding_verification_review = cls(
            app=app,
            deployment_id=deployment_id,
            scope=scope,
            service_targets=service_targets,
            reviewed_at=reviewed_at,
            ready=ready,
            review_digest=review_digest,
            object_storage_write_required=object_storage_write_required,
            queue_publish_required=queue_publish_required,
            inventory=inventory,
            policy=policy,
            plan=plan,
            blockers=blockers,
            observed_check=observed_check,
        )

        binding_verification_review.additional_properties = d
        return binding_verification_review

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
