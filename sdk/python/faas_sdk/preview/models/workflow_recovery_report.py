from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_history_entry import WorkflowRecoveryHistoryEntry
    from ..models.workflow_recovery_report_blocker import WorkflowRecoveryReportBlocker
    from ..models.workflow_recovery_report_group import WorkflowRecoveryReportGroup
    from ..models.workflow_recovery_report_totals import WorkflowRecoveryReportTotals


T = TypeVar("T", bound="WorkflowRecoveryReport")


@_attrs_define
class WorkflowRecoveryReport:
    """Observed recovery counts with explicit retention and scan coverage limitations."""

    observed_at: datetime.datetime
    created_after: datetime.datetime
    created_before: datetime.datetime
    scanned_entries: int
    durable_entries: int
    truncated: bool
    coverage_notes: list[str]
    totals: WorkflowRecoveryReportTotals
    """Observed event-window counts and matched retry timing, without causal or live-state inference."""
    groups: list[WorkflowRecoveryReportGroup]
    blockers: list[WorkflowRecoveryReportBlocker]
    evidence: list[WorkflowRecoveryHistoryEntry]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        created_after = self.created_after.isoformat()

        created_before = self.created_before.isoformat()

        scanned_entries = self.scanned_entries

        durable_entries = self.durable_entries

        truncated = self.truncated

        coverage_notes = self.coverage_notes

        totals = self.totals.to_dict()

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "created_after": created_after,
                "created_before": created_before,
                "scanned_entries": scanned_entries,
                "durable_entries": durable_entries,
                "truncated": truncated,
                "coverage_notes": coverage_notes,
                "totals": totals,
                "groups": groups,
                "blockers": blockers,
                "evidence": evidence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_history_entry import WorkflowRecoveryHistoryEntry
        from ..models.workflow_recovery_report_blocker import WorkflowRecoveryReportBlocker
        from ..models.workflow_recovery_report_group import WorkflowRecoveryReportGroup
        from ..models.workflow_recovery_report_totals import WorkflowRecoveryReportTotals

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        created_after = datetime.datetime.fromisoformat(d.pop("created_after"))

        created_before = datetime.datetime.fromisoformat(d.pop("created_before"))

        scanned_entries = d.pop("scanned_entries")

        durable_entries = d.pop("durable_entries")

        truncated = d.pop("truncated")

        coverage_notes = cast(list[str], d.pop("coverage_notes"))

        totals = WorkflowRecoveryReportTotals.from_dict(d.pop("totals"))

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = WorkflowRecoveryReportGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = WorkflowRecoveryReportBlocker.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = WorkflowRecoveryHistoryEntry.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        workflow_recovery_report = cls(
            observed_at=observed_at,
            created_after=created_after,
            created_before=created_before,
            scanned_entries=scanned_entries,
            durable_entries=durable_entries,
            truncated=truncated,
            coverage_notes=coverage_notes,
            totals=totals,
            groups=groups,
            blockers=blockers,
            evidence=evidence,
        )

        workflow_recovery_report.additional_properties = d
        return workflow_recovery_report

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
