from __future__ import annotations
"""Application-evaluated invariant facts and targeted workflow blockers."""
from dataclasses import asdict, dataclass, replace
from attrs import evolve
from .business_decisions import OperationBusinessDecision
from typing import TYPE_CHECKING
if TYPE_CHECKING:
    from .models.operation_workflow_blocker import OperationWorkflowBlocker

@dataclass(frozen=True)
class OperationBusinessInvariant:
    workflow: str
    instance_id: str
    state: str
    code: str
    version: str
    status: str
    description: str
    operations: list[str]

    def canonical(self):
        OperationBusinessDecision(self.workflow, self.instance_id, self.code, self.description, self.state, self.version).to_payload()
        if (len(self.code) > 54 or len(self.version.encode("utf-8")) > 64 or len(self.description.encode("utf-8")) > 256
            or self.status not in ("passed", "failed", "unknown") or not 1 <= len(self.operations) <= 16):
            raise ValueError("Invalid invariant bounds or status")
        operations = sorted(self.operations)
        if len(set(operations)) != len(operations): raise ValueError("Duplicate invariant action")
        for operation in operations:
            OperationBusinessDecision("invariant", "invariant", operation, "invariant", "invariant", "invariant").to_payload()
        return replace(self, operations=operations)

    def to_payload(self):
        return {"kind": "gregale.business-invariant.v1", "invariant": asdict(self.canonical())}

    def apply(self, current: list[OperationWorkflowBlocker]) -> list[OperationWorkflowBlocker]:
        from .models.operation_workflow_blocker import OperationWorkflowBlocker
        invariant = self.canonical()
        code = "invariant-" + self.code
        result = [evolve(blocker) for blocker in current if blocker.code != code]
        if self.status != "passed":
            for operation in invariant.operations:
                blocker = OperationWorkflowBlocker(code=code, operation=operation,
                    description=f"Invariant {self.code} ({self.version}) {self.status}: {self.description}")
                prior = next((b for b in current if b.code == code and b.operation == operation), None)
                if prior is not None: blocker.first_observed_at = prior.first_observed_at
                result.append(blocker)
        if len(result) > 16: raise ValueError("Invariant update exceeds workflow blocker limit")
        return result
