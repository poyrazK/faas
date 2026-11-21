from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.run_budget_kind import RunBudgetKind, check_run_budget_kind

if TYPE_CHECKING:
    from ..models.run_budget_alert import RunBudgetAlert
    from ..models.run_budget_limits import RunBudgetLimits
    from ..models.run_budget_usage import RunBudgetUsage


T = TypeVar("T", bound="RunBudgetPolicy")


@_attrs_define
class RunBudgetPolicy:
    """Current revision, limits and usage for one budget."""

    kind: RunBudgetKind
    """Whether a budget applies to one customer or a named task."""
    ref: str
    revision: int
    enabled: bool
    limits: RunBudgetLimits
    """Optional concurrency, invocation and compute caps for a customer or task."""
    usage: RunBudgetUsage
    """Active reservations and metered usage in the current UTC-day window."""
    alerts: list[RunBudgetAlert]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        ref = self.ref

        revision = self.revision

        enabled = self.enabled

        limits = self.limits.to_dict()

        usage = self.usage.to_dict()

        alerts = []
        for alerts_item_data in self.alerts:
            alerts_item = alerts_item_data.to_dict()
            alerts.append(alerts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "ref": ref,
                "revision": revision,
                "enabled": enabled,
                "limits": limits,
                "usage": usage,
                "alerts": alerts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.run_budget_alert import RunBudgetAlert
        from ..models.run_budget_limits import RunBudgetLimits
        from ..models.run_budget_usage import RunBudgetUsage

        d = dict(src_dict)
        kind = check_run_budget_kind(d.pop("kind"))

        ref = d.pop("ref")

        revision = d.pop("revision")

        enabled = d.pop("enabled")

        limits = RunBudgetLimits.from_dict(d.pop("limits"))

        usage = RunBudgetUsage.from_dict(d.pop("usage"))

        alerts = []
        _alerts = d.pop("alerts")
        for alerts_item_data in _alerts:
            alerts_item = RunBudgetAlert.from_dict(alerts_item_data)

            alerts.append(alerts_item)

        run_budget_policy = cls(
            kind=kind,
            ref=ref,
            revision=revision,
            enabled=enabled,
            limits=limits,
            usage=usage,
            alerts=alerts,
        )

        run_budget_policy.additional_properties = d
        return run_budget_policy

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
