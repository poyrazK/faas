// Generated from a Gregale event contract.
import {EventContract} from "@gregale/sdk-node/preview/events/runtime";
export type Input = { readonly "import_id": string; };
export const contract = new EventContract<Input>({"version":1,"type":"import.requested","data":{"type":"object","properties":{"import_id":{"type":"string","minLength":36,"maxLength":36}},"required":["import_id"],"additionalProperties":false}} as const);
