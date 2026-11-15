"""Application snapshot comparison; business and report revisions are distinct."""
from dataclasses import asdict, dataclass
from .business_decisions import OperationBusinessDecision
from .types import UNSET

@dataclass(frozen=True)
class OperationWorkflowReconciliationInput:
    workflow: str
    instance_id: str
    authoritative_state: str
    source_revision: str
    expected_report_revision: int
    contract_version: int

    def validate(self) -> None:
        OperationBusinessDecision(self.workflow, self.instance_id, self.authoritative_state, "reconciliation", "reconciliation", self.source_revision).to_payload()
        if (type(self.expected_report_revision) is not int or not 0 <= self.expected_report_revision <= 9007199254740991
            or type(self.contract_version) is not int or not 1 <= self.contract_version <= 1000000):
            raise ValueError("Invalid reconciliation revision or contract version")

@dataclass(frozen=True)
class OperationWorkflowReconciliation(OperationWorkflowReconciliationInput):
    observed_state: str
    observed_report_revision: int
    observed_contract_version: int
    status: str

    def to_payload(self) -> dict:
        return {"kind": "gregale.workflow-reconciliation.v1", "reconciliation": asdict(self)}

    @property
    def refresh_needed(self) -> bool:
        return self.status in ("report_missing", "report_behind", "state_mismatch")

def evaluate_reconciliation(input: OperationWorkflowReconciliationInput, snapshot) -> OperationWorkflowReconciliation:
    input.validate()
    if snapshot is UNSET: snapshot = None
    state = None if snapshot is None or snapshot.state is UNSET else snapshot.state
    if snapshot is not None and (snapshot.workflow != input.workflow or snapshot.instance_id != input.instance_id):
        raise ValueError("Reconciliation snapshot identity differs")
    if state is not None and (state.workflow != input.workflow or state.instance_id != input.instance_id
        or state.contract_version != snapshot.contract_version or type(state.revision) is not int or not 1 <= state.revision <= 9007199254740991):
        raise ValueError("Invalid retained reconciliation state")
    version = snapshot.contract_version if snapshot is not None else 0
    revision = state.revision if state is not None else 0
    observed = state.state if state is not None else ""
    if version and version != input.contract_version: status = "contract_version_mismatch"
    elif not revision: status = "report_missing"
    elif input.expected_report_revision and revision > input.expected_report_revision: status = "report_ahead"
    elif input.expected_report_revision and revision < input.expected_report_revision: status = "report_behind"
    elif observed != input.authoritative_state: status = "state_mismatch"
    else: status = "in_sync"
    return OperationWorkflowReconciliation(**asdict(input), observed_state=observed, observed_report_revision=revision, observed_contract_version=version, status=status)
