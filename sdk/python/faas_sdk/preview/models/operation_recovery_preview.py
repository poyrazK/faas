from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_recovery_preview_resolution import (
    OperationRecoveryPreviewResolution,
    check_operation_recovery_preview_resolution,
)

if TYPE_CHECKING:
    from ..models.operation_recovery_inspection import OperationRecoveryInspection


T = TypeVar("T", bound="OperationRecoveryPreview")


@_attrs_define
class OperationRecoveryPreview:
    """Structural platform eligibility and planned reuse; no evidence that external effects can safely repeat."""

    inspection: OperationRecoveryInspection
    """Account-only durable recovery evidence with execution secrets and payloads omitted."""
    resolution: OperationRecoveryPreviewResolution
    eligible: bool
    evidence_required: bool
    blockers: list[str]
    reused_steps: list[str]
    reopened_steps: list[str]
    reusable_artifact_ids: list[UUID]
    publish_artifact_ids: list[UUID]
    starts_new_execution: bool
    clears_artifact_references: bool

    def to_dict(self) -> dict[str, Any]:
        inspection = self.inspection.to_dict()

        resolution: str = self.resolution

        eligible = self.eligible

        evidence_required = self.evidence_required

        blockers = self.blockers

        reused_steps = self.reused_steps

        reopened_steps = self.reopened_steps

        reusable_artifact_ids = []
        for reusable_artifact_ids_item_data in self.reusable_artifact_ids:
            reusable_artifact_ids_item = str(reusable_artifact_ids_item_data)
            reusable_artifact_ids.append(reusable_artifact_ids_item)

        publish_artifact_ids = []
        for publish_artifact_ids_item_data in self.publish_artifact_ids:
            publish_artifact_ids_item = str(publish_artifact_ids_item_data)
            publish_artifact_ids.append(publish_artifact_ids_item)

        starts_new_execution = self.starts_new_execution

        clears_artifact_references = self.clears_artifact_references

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "inspection": inspection,
                "resolution": resolution,
                "eligible": eligible,
                "evidence_required": evidence_required,
                "blockers": blockers,
                "reused_steps": reused_steps,
                "reopened_steps": reopened_steps,
                "reusable_artifact_ids": reusable_artifact_ids,
                "publish_artifact_ids": publish_artifact_ids,
                "starts_new_execution": starts_new_execution,
                "clears_artifact_references": clears_artifact_references,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_recovery_inspection import OperationRecoveryInspection

        d = dict(src_dict)
        inspection = OperationRecoveryInspection.from_dict(d.pop("inspection"))

        resolution = check_operation_recovery_preview_resolution(d.pop("resolution"))

        eligible = d.pop("eligible")

        evidence_required = d.pop("evidence_required")

        blockers = cast(list[str], d.pop("blockers"))

        reused_steps = cast(list[str], d.pop("reused_steps"))

        reopened_steps = cast(list[str], d.pop("reopened_steps"))

        reusable_artifact_ids = []
        _reusable_artifact_ids = d.pop("reusable_artifact_ids")
        for reusable_artifact_ids_item_data in _reusable_artifact_ids:
            reusable_artifact_ids_item = UUID(reusable_artifact_ids_item_data)

            reusable_artifact_ids.append(reusable_artifact_ids_item)

        publish_artifact_ids = []
        _publish_artifact_ids = d.pop("publish_artifact_ids")
        for publish_artifact_ids_item_data in _publish_artifact_ids:
            publish_artifact_ids_item = UUID(publish_artifact_ids_item_data)

            publish_artifact_ids.append(publish_artifact_ids_item)

        starts_new_execution = d.pop("starts_new_execution")

        clears_artifact_references = d.pop("clears_artifact_references")

        operation_recovery_preview = cls(
            inspection=inspection,
            resolution=resolution,
            eligible=eligible,
            evidence_required=evidence_required,
            blockers=blockers,
            reused_steps=reused_steps,
            reopened_steps=reopened_steps,
            reusable_artifact_ids=reusable_artifact_ids,
            publish_artifact_ids=publish_artifact_ids,
            starts_new_execution=starts_new_execution,
            clears_artifact_references=clears_artifact_references,
        )

        return operation_recovery_preview
