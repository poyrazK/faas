"""Application-reported business effects; no external effects are executed."""
from dataclasses import asdict, dataclass
import re
from .business_decisions import OperationBusinessDecision
@dataclass(frozen=True)
class OperationBusinessEffect:
    workflow: str
    instance_id: str
    state: str
    operation: str
    code: str
    version: str
    status: str
    description: str
    reference: str = ""
    amount_minor: int | None = None
    currency: str = ""
    def to_payload(self):
        OperationBusinessDecision(self.workflow,self.instance_id,self.code,self.description,self.state,self.version).to_payload()
        if not re.fullmatch(r"[a-z][a-z0-9-]{0,63}",self.operation) or len(self.version.encode("utf-8"))>64 or len(self.description.encode("utf-8"))>256 or self.status not in ("pending","failed","confirmed"):
            raise ValueError("Invalid effect operation, version, description, or status")
        if not isinstance(self.reference,str) or len(self.reference.encode("utf-8"))>256 or any(ord(c)<32 or ord(c)==127 for c in self.reference) or self.status=="confirmed" and not self.reference.strip():
            raise ValueError("Confirmed effects require a bounded reference without control characters")
        if (self.amount_minor is None and self.currency or self.amount_minor is not None and
            (type(self.amount_minor) is not int or not 0<=self.amount_minor<=9007199254740991 or not re.fullmatch(r"[A-Z]{3}",self.currency))):
            raise ValueError("Effect amount requires nonnegative safe minor units and uppercase currency")
        data=asdict(self)
        if self.amount_minor is None: data.pop("amount_minor")
        if not self.reference: data.pop("reference")
        if not self.currency: data.pop("currency")
        return {"kind":"gregale.business-effect.v1","effect":data}
