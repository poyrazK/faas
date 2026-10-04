from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.global_runtime_quota_policy_plan import (
    GlobalRuntimeQuotaPolicyPlan,
    check_global_runtime_quota_policy_plan,
)

T = TypeVar("T", bound="GlobalRuntimeQuotaPolicy")


@_attrs_define
class GlobalRuntimeQuotaPolicy:
    """Canonical tenant plan entitlement snapshot used to bound retained allocations."""

    id: str
    """Digest of the canonical immutable tenant entitlement snapshot."""
    plan: GlobalRuntimeQuotaPolicyPlan
    concurrency: int
    peak_concurrency: int
    """Steady capacity plus the plan's rollout overlap grant."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        plan: str = self.plan

        concurrency = self.concurrency

        peak_concurrency = self.peak_concurrency

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "plan": plan,
                "concurrency": concurrency,
                "peak_concurrency": peak_concurrency,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        plan = check_global_runtime_quota_policy_plan(d.pop("plan"))

        concurrency = d.pop("concurrency")

        peak_concurrency = d.pop("peak_concurrency")

        global_runtime_quota_policy = cls(
            id=id,
            plan=plan,
            concurrency=concurrency,
            peak_concurrency=peak_concurrency,
        )

        global_runtime_quota_policy.additional_properties = d
        return global_runtime_quota_policy

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
