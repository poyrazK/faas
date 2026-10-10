"""Bounded application decision evidence using the transactional milestone outbox."""
from dataclasses import asdict, dataclass
import re

@dataclass(frozen=True)
class OperationBusinessDecision:
    workflow: str
    instance_id: str
    code: str
    description: str
    rule_id: str
    rule_version: str

    def to_payload(self) -> dict:
        for value, limit in ((self.workflow, 63), (self.code, 64), (self.rule_id, 64)):
            if not isinstance(value, str) or len(value) > limit or not re.fullmatch(r"[a-z][a-z0-9-]*", value):
                raise ValueError("Decision workflow, code, and rule ID must be bounded lowercase slugs")
        for value, limit in ((self.instance_id, 256), (self.description, 1024), (self.rule_version, 128)):
            if not isinstance(value, str) or not value.strip() or len(value.encode("utf-8")) > limit or any(ord(char) < 32 or ord(char) == 127 for char in value):
                raise ValueError("Decision fields must be bounded nonempty text without control characters")
        return {"kind": "gregale.business-decision.v1", "decision": asdict(self)}
