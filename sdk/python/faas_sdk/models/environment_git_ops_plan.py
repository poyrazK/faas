from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_ops_change import EnvironmentGitOpsChange
    from ..models.environment_workload_retirement_impact import EnvironmentWorkloadRetirementImpact


T = TypeVar("T", bound="EnvironmentGitOpsPlan")


@_attrs_define
class EnvironmentGitOpsPlan:
    """Observation-bound plan; blocking reasons prevent mutation and overrides prevent convergence."""

    manager: str
    revision: str
    generation: int
    desired_digest: str
    observed_version: int
    plan_hash: str
    changes: list[EnvironmentGitOpsChange]
    blocking_reasons: list[str]
    retirement_impacts: list[EnvironmentWorkloadRetirementImpact] | Unset = UNSET
    """Informational review-time work counts; counts do not authorize deletion or prevent busy workloads from
    retiring. Original identities and configuration remain protected by the plan hash."""
    commit_sha: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        manager = self.manager

        revision = self.revision

        generation = self.generation

        desired_digest = self.desired_digest

        observed_version = self.observed_version

        plan_hash = self.plan_hash

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        blocking_reasons = self.blocking_reasons

        retirement_impacts: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.retirement_impacts, Unset):
            retirement_impacts = []
            for retirement_impacts_item_data in self.retirement_impacts:
                retirement_impacts_item = retirement_impacts_item_data.to_dict()
                retirement_impacts.append(retirement_impacts_item)

        commit_sha = self.commit_sha

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "manager": manager,
                "revision": revision,
                "generation": generation,
                "desired_digest": desired_digest,
                "observed_version": observed_version,
                "plan_hash": plan_hash,
                "changes": changes,
                "blocking_reasons": blocking_reasons,
            }
        )
        if retirement_impacts is not UNSET:
            field_dict["retirement_impacts"] = retirement_impacts
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_ops_change import EnvironmentGitOpsChange
        from ..models.environment_workload_retirement_impact import EnvironmentWorkloadRetirementImpact

        d = dict(src_dict)
        manager = d.pop("manager")

        revision = d.pop("revision")

        generation = d.pop("generation")

        desired_digest = d.pop("desired_digest")

        observed_version = d.pop("observed_version")

        plan_hash = d.pop("plan_hash")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = EnvironmentGitOpsChange.from_dict(changes_item_data)

            changes.append(changes_item)

        blocking_reasons = cast(list[str], d.pop("blocking_reasons"))

        _retirement_impacts = d.pop("retirement_impacts", UNSET)
        retirement_impacts: list[EnvironmentWorkloadRetirementImpact] | Unset = UNSET
        if _retirement_impacts is not UNSET:
            retirement_impacts = []
            for retirement_impacts_item_data in _retirement_impacts:
                retirement_impacts_item = EnvironmentWorkloadRetirementImpact.from_dict(retirement_impacts_item_data)

                retirement_impacts.append(retirement_impacts_item)

        commit_sha = d.pop("commit_sha", UNSET)

        environment_git_ops_plan = cls(
            manager=manager,
            revision=revision,
            generation=generation,
            desired_digest=desired_digest,
            observed_version=observed_version,
            plan_hash=plan_hash,
            changes=changes,
            blocking_reasons=blocking_reasons,
            retirement_impacts=retirement_impacts,
            commit_sha=commit_sha,
        )

        environment_git_ops_plan.additional_properties = d
        return environment_git_ops_plan

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
