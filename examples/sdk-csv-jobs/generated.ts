// Generated from a Gregale job contract.
import {JobContract} from "@gregale/sdk-node/jobs/runtime";
export type Input = { readonly "row": number; readonly "email": string; readonly "fail_once"?: boolean; };
export type Output = { readonly "row": number; readonly "saved": boolean; readonly "rejection": "invalid_email" | "none"; };
export const contract = new JobContract<Input, Output>({"version":1,"name":"csv-import","input":{"type":"object","properties":{"row":{"type":"integer","minimum":0},"email":{"type":"string","minLength":3,"maxLength":10000},"fail_once":{"type":"boolean"}},"required":["row","email"],"additionalProperties":false},"output":{"type":"object","properties":{"row":{"type":"integer","minimum":0},"saved":{"type":"boolean"},"rejection":{"type":"string","enum":["invalid_email","none"]}},"required":["row","saved","rejection"],"additionalProperties":false}} as const);
