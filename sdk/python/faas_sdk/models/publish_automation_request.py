from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_check_evidence import AutomationCheckEvidence


T = TypeVar("T", bound="PublishAutomationRequest")


@_attrs_define
class PublishAutomationRequest:
    """Saved draft revision to publish, with explicit YAML takeover when needed."""

    expected_version: int
    """Current saved draft revision to validate and publish."""
    check_receipt: str | Unset = UNSET
    """Server-issued receipt required by scenarios or coverage policy; stale, expired, cross-identity and reused
    receipts are rejected."""
    check_evidence: AutomationCheckEvidence | Unset = UNSET
    """Simulation check metadata. server_verified is set only for server-issued publishing receipts; legacy or
    plain client evidence is unverified. Bound to the saved draft hash and version. Contains metadata only; no
    sample inputs, outputs, or failure text. Checked time must be within the past day (five minutes of future clock
    skew allowed)."""
    take_over_manifest: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        check_receipt = self.check_receipt

        check_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.check_evidence, Unset):
            check_evidence = self.check_evidence.to_dict()

        take_over_manifest = self.take_over_manifest

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
            }
        )
        if check_receipt is not UNSET:
            field_dict["check_receipt"] = check_receipt
        if check_evidence is not UNSET:
            field_dict["check_evidence"] = check_evidence
        if take_over_manifest is not UNSET:
            field_dict["take_over_manifest"] = take_over_manifest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_check_evidence import AutomationCheckEvidence

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        check_receipt = d.pop("check_receipt", UNSET)

        _check_evidence = d.pop("check_evidence", UNSET)
        check_evidence: AutomationCheckEvidence | Unset
        if isinstance(_check_evidence, Unset):
            check_evidence = UNSET
        else:
            check_evidence = AutomationCheckEvidence.from_dict(_check_evidence)

        take_over_manifest = d.pop("take_over_manifest", UNSET)

        publish_automation_request = cls(
            expected_version=expected_version,
            check_receipt=check_receipt,
            check_evidence=check_evidence,
            take_over_manifest=take_over_manifest,
        )

        return publish_automation_request
