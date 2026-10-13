from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding


T = TypeVar("T", bound="OperationWorkflowObligationFulfillmentRequest")


@_attrs_define
class OperationWorkflowObligationFulfillmentRequest:
    """Explicit fulfillment of one current bound case/cycle. Must preserve source status, owner, note and deadline and
    cannot combine with target binding, response or shared completion edits. The full binding fences replacement or
    rebinding. Evidence must be a retained application report of the exact target transition, published after binding;
    current readiness is insufficient.

    """

    binding: OperationWorkflowObligationTargetBinding
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    reason: str
    evidence_operation_id: UUID
    evidence_report_id: str

    def to_dict(self) -> dict[str, Any]:
        binding = self.binding.to_dict()

        reason = self.reason

        evidence_operation_id = str(self.evidence_operation_id)

        evidence_report_id = self.evidence_report_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "binding": binding,
                "reason": reason,
                "evidence_operation_id": evidence_operation_id,
                "evidence_report_id": evidence_report_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding

        d = dict(src_dict)
        binding = OperationWorkflowObligationTargetBinding.from_dict(d.pop("binding"))

        reason = d.pop("reason")

        evidence_operation_id = UUID(d.pop("evidence_operation_id"))

        evidence_report_id = d.pop("evidence_report_id")

        operation_workflow_obligation_fulfillment_request = cls(
            binding=binding,
            reason=reason,
            evidence_operation_id=evidence_operation_id,
            evidence_report_id=evidence_report_id,
        )

        return operation_workflow_obligation_fulfillment_request
