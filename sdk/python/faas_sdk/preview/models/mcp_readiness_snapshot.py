from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_evidence import MCPReadinessEvidence
    from ..models.mcp_readiness_monitor_status import MCPReadinessMonitorStatus
    from ..models.mcp_readiness_override import MCPReadinessOverride
    from ..models.mcp_readiness_policy import MCPReadinessPolicy


T = TypeVar("T", bound="MCPReadinessSnapshot")


@_attrs_define
class MCPReadinessSnapshot:
    """Environment readiness policy together with retained evidence and overrides."""

    policy: MCPReadinessPolicy
    """Saved environment readiness requirements with revision and audit context."""
    evidence: list[MCPReadinessEvidence]
    overrides: list[MCPReadinessOverride]
    monitoring: list[MCPReadinessMonitorStatus] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy.to_dict()

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        overrides = []
        for overrides_item_data in self.overrides:
            overrides_item = overrides_item_data.to_dict()
            overrides.append(overrides_item)

        monitoring: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.monitoring, Unset):
            monitoring = []
            for monitoring_item_data in self.monitoring:
                monitoring_item = monitoring_item_data.to_dict()
                monitoring.append(monitoring_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policy": policy,
                "evidence": evidence,
                "overrides": overrides,
            }
        )
        if monitoring is not UNSET:
            field_dict["monitoring"] = monitoring

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_evidence import MCPReadinessEvidence
        from ..models.mcp_readiness_monitor_status import MCPReadinessMonitorStatus
        from ..models.mcp_readiness_override import MCPReadinessOverride
        from ..models.mcp_readiness_policy import MCPReadinessPolicy

        d = dict(src_dict)
        policy = MCPReadinessPolicy.from_dict(d.pop("policy"))

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = MCPReadinessEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        overrides = []
        _overrides = d.pop("overrides")
        for overrides_item_data in _overrides:
            overrides_item = MCPReadinessOverride.from_dict(overrides_item_data)

            overrides.append(overrides_item)

        _monitoring = d.pop("monitoring", UNSET)
        monitoring: list[MCPReadinessMonitorStatus] | Unset = UNSET
        if _monitoring is not UNSET:
            monitoring = []
            for monitoring_item_data in _monitoring:
                monitoring_item = MCPReadinessMonitorStatus.from_dict(monitoring_item_data)

                monitoring.append(monitoring_item)

        mcp_readiness_snapshot = cls(
            policy=policy,
            evidence=evidence,
            overrides=overrides,
            monitoring=monitoring,
        )

        return mcp_readiness_snapshot
