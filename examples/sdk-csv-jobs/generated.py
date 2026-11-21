# Generated from a Gregale job contract.
import json
from typing import Literal, NotRequired, TypedDict

from faas_sdk.job_runtime import JobContract

Input = TypedDict("Input", {"row": int, "email": str, "fail_once": NotRequired[bool]})
Output = TypedDict(
    "Output", {"row": int, "saved": bool, "rejection": Literal["invalid_email", "none"]}
)
contract: JobContract[Input, Output] = JobContract(
    json.loads(
        '{"version":1,"name":"csv-import","input":{"type":"object","properties":{"row":{"type":"integer","minimum":0},"email":{"type":"string","minLength":3,"maxLength":10000},"fail_once":{"type":"boolean"}},"required":["row","email"],"additionalProperties":false},"output":{"type":"object","properties":{"row":{"type":"integer","minimum":0},"saved":{"type":"boolean"},"rejection":{"type":"string","enum":["invalid_email","none"]}},"required":["row","saved","rejection"],"additionalProperties":false}}'
    )
)
