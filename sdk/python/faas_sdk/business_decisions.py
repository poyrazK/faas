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
    conditions: list[dict] | None = None

    def to_payload(self) -> dict:
        for value, limit in ((self.workflow, 63), (self.code, 64), (self.rule_id, 64)):
            if not isinstance(value, str) or len(value) > limit or not re.fullmatch(r"[a-z][a-z0-9-]*", value):
                raise ValueError("Decision workflow, code, and rule ID must be bounded lowercase slugs")
        for value, limit in ((self.instance_id, 256), (self.description, 1024), (self.rule_version, 128)):
            if not isinstance(value, str) or not value.strip() or len(value.encode("utf-8")) > limit or any(ord(char) < 32 or ord(char) == 127 for char in value):
                raise ValueError("Decision fields must be bounded nonempty text without control characters")
        decision = asdict(self)
        decision.pop("conditions", None)
        if self.conditions:
            if not isinstance(self.conditions, list) or len(self.conditions)>16:
                raise ValueError("Business conditions exceed limit")
            seen = set()
            conditions = []
            for c in self.conditions:
                if not isinstance(c, dict) or set(c) != {"id", "description", "result"} or not isinstance(c["id"], str) or not re.fullmatch(r"[a-z][a-z0-9-]{0,63}", c["id"]) or c["id"] in seen or not isinstance(c["description"], str) or not c["description"].strip() or len(c["description"].encode("utf-8"))>1024 or any(ord(ch)<32 or ord(ch)==127 for ch in c["description"]) or c["result"] not in ("pass", "fail", "unknown"):
                    raise ValueError("Invalid business condition result")
                seen.add(c["id"])
                conditions.append(dict(c))
            decision["conditions"] = sorted(conditions, key=lambda c: c["id"])
        elif self.conditions is not None and not isinstance(self.conditions, list):
            raise ValueError("Business conditions must be a list")
        return {"kind": "gregale.business-decision.v1", "decision": decision}
