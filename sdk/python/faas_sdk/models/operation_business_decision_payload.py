from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_business_decision_payload_kind import (
    OperationBusinessDecisionPayloadKind,
    check_operation_business_decision_payload_kind,
)

if TYPE_CHECKING:
    from ..models.operation_business_decision import OperationBusinessDecision


T = TypeVar("T", bound="OperationBusinessDecisionPayload")


@_attrs_define
class OperationBusinessDecisionPayload:
    """Versioned application decision evidence carried in a declared milestone payload. Must match its declared workflow
    step and instance. Uses existing milestone transaction, publication, ownership, and retention boundaries.

    """

    kind: OperationBusinessDecisionPayloadKind
    decision: OperationBusinessDecision

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "decision": decision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_decision import OperationBusinessDecision

        d = dict(src_dict)
        kind = check_operation_business_decision_payload_kind(d.pop("kind"))

        decision = OperationBusinessDecision.from_dict(d.pop("decision"))

        operation_business_decision_payload = cls(
            kind=kind,
            decision=decision,
        )

        return operation_business_decision_payload
