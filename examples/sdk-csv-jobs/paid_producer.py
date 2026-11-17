"""Application SQL only: one paid import intent despite duplicate/reordered events."""

from __future__ import annotations

from event_generated import Input, contract
from faas_sdk.preview import (
    StripeWebhookAdapter,
    StripeWebhookIgnore,
    StripeWebhookStage,
)


def paid_csv_adapter(*, pool, scope, endpoint_id, token, verify):
    async def authorize(tx, event, actor, action):
        cursor = await tx.execute(
            "SELECT * FROM sdk_csv_payments WHERE payment_id=%s FOR UPDATE",
            [event.data_object["id"]],
        )
        row = await cursor.fetchone()
        return (
            row["owner_id"]
            if row
            and row["enabled"]
            and row["owner_id"] == actor
            and row["customer_id"] == event.data_object.get("customer")
            else None
        )

    async def apply(tx, event, owner, actor):
        cursor = await tx.execute(
            "SELECT * FROM sdk_csv_payments WHERE payment_id=%s",
            [event.data_object["id"]],
        )
        row = await cursor.fetchone()
        if row["state"] == "cancelled":
            return StripeWebhookIgnore(reason="cancelled")
        if row["state"] == "paid":
            return StripeWebhookIgnore(reason="already_applied")
        if event.type != "payment_intent.succeeded":
            return StripeWebhookIgnore(reason="not_applicable")
        payment = event.data_object
        if (
            payment.get("object") != "payment_intent"
            or payment.get("status") != "succeeded"
            or payment.get("amount_received") != row["amount_cents"]
            or payment.get("currency") != row["currency"]
        ):
            raise ValueError("payment_mismatch")
        import_id = str(row["import_id"])
        if (
            row["input"]["import_id"] != import_id
            or row["input"]["source"]["owner_id"] != row["owner_id"]
        ):
            raise ValueError("invalid_import_owner")
        await tx.execute(
            "UPDATE sdk_csv_payments SET state='paid',applications=applications+1 WHERE payment_id=%s",
            [payment["id"]],
        )
        from psycopg.types.json import Jsonb

        await tx.execute(
            "INSERT INTO sdk_csv_event_inputs VALUES (%s,%s,%s)",
            [import_id, row["owner_id"], Jsonb(row["input"])],
        )
        return StripeWebhookStage(input=Input(import_id=import_id))

    return StripeWebhookAdapter(
        contract,
        name="paid-csv-imports",
        scope=scope,
        endpoint_id=endpoint_id,
        delivery_path="/webhooks/stripe",
        api_version="2024-06-20",
        livemode=False,
        event_types=["payment_intent.succeeded", "payment_intent.payment_failed"],
        pool=pool,
        token=token,
        verify=verify,
        authorize=authorize,
        apply=apply,
    )
