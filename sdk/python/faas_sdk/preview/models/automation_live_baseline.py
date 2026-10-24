from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_live_baseline_policy import AutomationLiveBaselinePolicy
    from ..models.automation_live_suite_summary import AutomationLiveSuiteSummary


T = TypeVar("T", bound="AutomationLiveBaseline")


@_attrs_define
class AutomationLiveBaseline:
    """Immutable attributed receipt binding a named baseline revision to its retained suite and reviewed policy."""

    promotion_id: UUID
    app_id: UUID
    name: str
    baseline_name: str
    revision: int
    actor_account_id: UUID
    promoted_at: datetime.datetime
    suite: AutomationLiveSuiteSummary
    """Retained suite identity and linked-run counts. Listing is not an assertion verdict."""
    policy: AutomationLiveBaselinePolicy
    """Version 2 required-case coverage and evidence-age policy reviewed during baseline promotion."""
    policy_hash: str
    cases_passed: int
    oldest_run_created_at: datetime.datetime
    evidence_age_seconds: float
    previous_promotion_id: UUID | Unset = UNSET
    actor_api_key_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        promotion_id = str(self.promotion_id)

        app_id = str(self.app_id)

        name = self.name

        baseline_name = self.baseline_name

        revision = self.revision

        actor_account_id = str(self.actor_account_id)

        promoted_at = self.promoted_at.isoformat()

        suite = self.suite.to_dict()

        policy = self.policy.to_dict()

        policy_hash = self.policy_hash

        cases_passed = self.cases_passed

        oldest_run_created_at = self.oldest_run_created_at.isoformat()

        evidence_age_seconds = self.evidence_age_seconds

        previous_promotion_id: str | Unset = UNSET
        if not isinstance(self.previous_promotion_id, Unset):
            previous_promotion_id = str(self.previous_promotion_id)

        actor_api_key_id: str | Unset = UNSET
        if not isinstance(self.actor_api_key_id, Unset):
            actor_api_key_id = str(self.actor_api_key_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "promotion_id": promotion_id,
                "app_id": app_id,
                "name": name,
                "baseline_name": baseline_name,
                "revision": revision,
                "actor_account_id": actor_account_id,
                "promoted_at": promoted_at,
                "suite": suite,
                "policy": policy,
                "policy_hash": policy_hash,
                "cases_passed": cases_passed,
                "oldest_run_created_at": oldest_run_created_at,
                "evidence_age_seconds": evidence_age_seconds,
            }
        )
        if previous_promotion_id is not UNSET:
            field_dict["previous_promotion_id"] = previous_promotion_id
        if actor_api_key_id is not UNSET:
            field_dict["actor_api_key_id"] = actor_api_key_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_baseline_policy import AutomationLiveBaselinePolicy
        from ..models.automation_live_suite_summary import AutomationLiveSuiteSummary

        d = dict(src_dict)
        promotion_id = UUID(d.pop("promotion_id"))

        app_id = UUID(d.pop("app_id"))

        name = d.pop("name")

        baseline_name = d.pop("baseline_name")

        revision = d.pop("revision")

        actor_account_id = UUID(d.pop("actor_account_id"))

        promoted_at = datetime.datetime.fromisoformat(d.pop("promoted_at"))

        suite = AutomationLiveSuiteSummary.from_dict(d.pop("suite"))

        policy = AutomationLiveBaselinePolicy.from_dict(d.pop("policy"))

        policy_hash = d.pop("policy_hash")

        cases_passed = d.pop("cases_passed")

        oldest_run_created_at = datetime.datetime.fromisoformat(d.pop("oldest_run_created_at"))

        evidence_age_seconds = d.pop("evidence_age_seconds")

        _previous_promotion_id = d.pop("previous_promotion_id", UNSET)
        previous_promotion_id: UUID | Unset
        if isinstance(_previous_promotion_id, Unset):
            previous_promotion_id = UNSET
        else:
            previous_promotion_id = UUID(_previous_promotion_id)

        _actor_api_key_id = d.pop("actor_api_key_id", UNSET)
        actor_api_key_id: UUID | Unset
        if isinstance(_actor_api_key_id, Unset):
            actor_api_key_id = UNSET
        else:
            actor_api_key_id = UUID(_actor_api_key_id)

        automation_live_baseline = cls(
            promotion_id=promotion_id,
            app_id=app_id,
            name=name,
            baseline_name=baseline_name,
            revision=revision,
            actor_account_id=actor_account_id,
            promoted_at=promoted_at,
            suite=suite,
            policy=policy,
            policy_hash=policy_hash,
            cases_passed=cases_passed,
            oldest_run_created_at=oldest_run_created_at,
            evidence_age_seconds=evidence_age_seconds,
            previous_promotion_id=previous_promotion_id,
            actor_api_key_id=actor_api_key_id,
        )

        return automation_live_baseline
