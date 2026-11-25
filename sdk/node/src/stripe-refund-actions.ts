/** Stripe API v1 refunds through a pinned Gregale integration, without provider credentials. */
import { JobContract, jobValueHash } from "./job-runtime.js";
import {
  OutboundActionError,
  type OutboundActionProvider,
  type OutboundActionObservation,
  type OutboundActionIntent,
} from "./outbound-actions.js";
export interface StripeRefundInput {
  readonly payment_intent: string;
  readonly amount_minor: number;
  readonly currency: string;
  readonly reason: "requested_by_customer" | "duplicate";
}
export interface StripeRefundResult {
  readonly refund_id: string;
  readonly payment_intent: string;
  readonly amount_minor: number;
  readonly currency: string;
  readonly status:
    | "succeeded"
    | "pending"
    | "requires_action"
    | "failed"
    | "canceled";
}
export const stripeRefundContract = new JobContract<
  StripeRefundInput,
  StripeRefundResult
>({
  version: 1,
  name: "stripe-refund",
  input: {
    type: "object",
    properties: {
      payment_intent: { type: "string", minLength: 4, maxLength: 131 },
      amount_minor: {
        type: "integer",
        minimum: 1,
        maximum: Number.MAX_SAFE_INTEGER,
      },
      currency: { type: "string", minLength: 3, maxLength: 3 },
      reason: { type: "string", enum: ["requested_by_customer", "duplicate"] },
    },
    required: ["payment_intent", "amount_minor", "currency", "reason"],
    additionalProperties: false,
  },
  output: {
    type: "object",
    properties: {
      refund_id: { type: "string" },
      payment_intent: { type: "string" },
      amount_minor: {
        type: "integer",
        minimum: 1,
        maximum: Number.MAX_SAFE_INTEGER,
      },
      currency: { type: "string" },
      status: {
        type: "string",
        enum: ["succeeded", "pending", "requires_action", "failed", "canceled"],
      },
    },
    required: [
      "refund_id",
      "payment_intent",
      "amount_minor",
      "currency",
      "status",
    ],
    additionalProperties: false,
  },
});
export function stripeRefundActionProvider(options: {
  accountId: string;
  apiVersion: string;
  maxPages?: number;
}): OutboundActionProvider<StripeRefundInput, StripeRefundResult> {
  const account = options.accountId,
    version = options.apiVersion,
    pages = options.maxPages ?? 5;
  if (
    !/^acct_[A-Za-z0-9]{1,128}$/.test(account) ||
    !/^\d{4}-\d{2}-\d{2}\.[a-z]+$/.test(version) ||
    !Number.isSafeInteger(pages) ||
    pages < 1 ||
    pages > 10
  )
    throw new OutboundActionError("invalid_input");
  const unknown = (
    code: string,
  ): OutboundActionObservation<StripeRefundResult> => ({
    state: "unknown",
    code,
  });
  function refund(
    raw: any,
    intent: OutboundActionIntent<StripeRefundInput>,
  ): OutboundActionObservation<StripeRefundResult> {
    const i = intent.input;
    if (
      !raw ||
      raw.object !== "refund" ||
      !/^re_[A-Za-z0-9]{1,128}$/.test(raw.id) ||
      raw.payment_intent !== i.payment_intent ||
      raw.amount !== i.amount_minor ||
      raw.currency !== i.currency ||
      raw.metadata?.gregale_action_id !== intent.identity ||
      raw.metadata?.gregale_input_sha256 !== intent.input_sha256 ||
      ![
        "succeeded",
        "pending",
        "requires_action",
        "failed",
        "canceled",
      ].includes(raw.status)
    )
      return { state: "needs_review", code: "provider_binding_mismatch" };
    return {
      state:
        raw.status === "succeeded"
          ? "succeeded"
          : raw.status === "pending"
            ? "pending"
            : raw.status === "requires_action"
              ? "needs_review"
              : "failed",
      code: "refund_" + raw.status,
      reference: raw.id,
      result: {
        refund_id: raw.id,
        payment_intent: raw.payment_intent,
        amount_minor: raw.amount,
        currency: raw.currency,
        status: raw.status,
      },
    };
  }
  return {
    name: "stripe_refund",
    configuration: {
      account_id: account,
      api_version: version,
      max_pages: pages,
    },
    request(raw, identity) {
      const input = stripeRefundContract.input(raw);
      if (
        !/^pi_[A-Za-z0-9]{1,128}$/.test(input.payment_intent) ||
        !/^[a-z]{3}$/.test(input.currency)
      )
        throw new OutboundActionError("invalid_input");
      const form = new URLSearchParams([
        ["payment_intent", input.payment_intent],
        ["amount", String(input.amount_minor)],
        ["currency", input.currency],
        ["reason", input.reason],
        ["metadata[gregale_action_id]", identity],
        ["metadata[gregale_input_sha256]", jobValueHash(input)],
      ]);
      return {
        path: "/v1/refunds",
        content_type: "application/x-www-form-urlencoded",
        body: form.toString(),
        api_version: version,
      };
    },
    async verify(reader) {
      const r = await reader.get("/v1/account");
      return r.status === 200 && (r.body as any)?.id === account;
    },
    decode(response, intent) {
      if (response.status >= 200 && response.status < 300)
        return refund(response.body, intent);
      if ([400, 401, 402, 403, 404, 422].includes(response.status))
        return { state: "failed", code: "provider_rejected" };
      return unknown("dispatch_unconfirmed");
    },
    async reconcile(reader, record) {
      if (record.outcome.reference) {
        const r = await reader.get("/v1/refunds/" + record.outcome.reference);
        if (r.status !== 200) return unknown("provider_read_unconfirmed");
        return refund(r.body, record.intent);
      }
      let cursor = "",
        match: any,
        seen = new Set<string>();
      for (let page = 0; page < pages; page++) {
        const query =
            "?payment_intent=" +
            record.intent.input.payment_intent +
            "&limit=100" +
            (cursor ? "&starting_after=" + cursor : ""),
          r = await reader.get("/v1/refunds" + query),
          body = r.body as any;
        if (r.status !== 200) return unknown("provider_read_unconfirmed");
        if (
          !body ||
          body.object !== "list" ||
          !Array.isArray(body.data) ||
          body.data.length > 100 ||
          typeof body.has_more !== "boolean"
        )
          return { state: "needs_review", code: "provider_response_invalid" };
        for (const item of body.data) {
          if (
            !item ||
            !/^re_[A-Za-z0-9]{1,128}$/.test(item.id) ||
            seen.has(item.id)
          )
            return { state: "needs_review", code: "provider_response_invalid" };
          seen.add(item.id);
          if (item.metadata?.gregale_action_id === record.intent.identity) {
            if (match)
              return {
                state: "needs_review",
                code: "multiple_provider_matches",
              };
            match = item;
          }
        }
        if (!body.has_more)
          return match
            ? refund(match, record.intent)
            : unknown("provider_not_observed");
        if (!body.data.length)
          return { state: "needs_review", code: "provider_response_invalid" };
        cursor = body.data.at(-1).id;
      }
      return unknown("reconciliation_limit");
    },
  };
}
