from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_report_totals import WorkflowRecoveryReportTotals


T = TypeVar("T", bound="WorkflowRecoveryReportGroup")


@_attrs_define
class WorkflowRecoveryReportGroup:
    """Recovery counts for one workflow association and policy configuration version."""

    workflow: str
    policy_version: int
    totals: WorkflowRecoveryReportTotals
    """Observed event-window counts and matched retry timing, without causal or live-state inference."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        policy_version = self.policy_version

        totals = self.totals.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workflow": workflow,
                "policy_version": policy_version,
                "totals": totals,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_report_totals import WorkflowRecoveryReportTotals

        d = dict(src_dict)
        workflow = d.pop("workflow")

        policy_version = d.pop("policy_version")

        totals = WorkflowRecoveryReportTotals.from_dict(d.pop("totals"))

        workflow_recovery_report_group = cls(
            workflow=workflow,
            policy_version=policy_version,
            totals=totals,
        )

        workflow_recovery_report_group.additional_properties = d
        return workflow_recovery_report_group

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
