"""Fresh action clients and autocommit SQL queries for the paid CSV application."""

from __future__ import annotations

import json
import os
import re
from contextlib import asynccontextmanager
from pathlib import Path

import psycopg
from faas_sdk.job_runtime import JobContract
from faas_sdk.preview import (
    JobPipelineClient,
    OutboundAction,
    OutboundActionActor,
    PipelineScope,
    PostgresOutboundActionStore,
    PostgresPipelineGateStore,
    PostgresPipelineToolStore,
    StripeRefundActionProvider,
    compensation_contracts,
    stripe_refund_contract,
)
from psycopg.rows import dict_row


@asynccontextmanager
async def backend(relay_url, review_base_url="http://127.0.0.1/"):
    scope = PipelineScope.from_dict(json.loads(os.environ["GREGALE_PIPELINE_SCOPE"]))

    async def query(sql, values):
        args = []

        def parameter(match):
            args.append(values[int(match[1]) - 1])
            return "%s"

        async with await psycopg.AsyncConnection.connect(
            os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row
        ) as conn:
            cursor = await conn.execute(re.sub(r"\$(\d+)", parameter, sql), args)
            return list(await cursor.fetchall()) if cursor.description else []

    cleanup = compensation_contracts(
        JobContract(
            json.loads(
                Path(__file__).with_name("compensation-contract.json").read_text()
            )
        ),
        "csv_cleanup",
    )["root"]

    async def allowed(id, input, owner):
        rows = await query(
            "SELECT r.*,p.owner_id AS payment_owner,p.state AS payment_state,p.enabled AS payment_enabled,p.amount_cents AS payment_amount,p.currency AS payment_currency FROM sdk_csv_refund_requests r JOIN sdk_csv_payments p ON p.payment_id=r.payment_id WHERE r.id=$1",
            [id],
        )
        row = rows[0] if rows else None
        if (
            not row
            or not row["enabled"]
            or not row["payment_enabled"]
            or row["payment_state"] != "paid"
            or row["owner_id"] != owner
            or row["payment_owner"] != owner
            or input["payment_intent"] != row["payment_id"]
            or input["amount_minor"] != row["amount_minor"]
            or input["currency"] != row["currency"]
            or input["currency"] != row["payment_currency"]
            or input["amount_minor"] > row["payment_amount"]
            or input["reason"] != "requested_by_customer"
        ):
            return False
        from faas_sdk.preview import PipelineCheckpoint

        async with JobPipelineClient(
            base_url=scope.base_url,
            scope=scope,
            owner_id=owner,
            token=os.environ["FAAS_TOKEN"],
            contract=cleanup,
        ) as client:
            result = await client.resume(
                PipelineCheckpoint.from_dict(row["recovery_checkpoint"])
            ).result(timeout=10)
            return result["state"] == "completed"

    async def authorize(id, input, actor, operation):
        return (
            OutboundActionActor(actor_id=actor, owner_id=actor)
            if await allowed(id, input, actor)
            else None
        )

    async def execution(intent, run_id):
        return await allowed(intent["request_id"], intent["input"], intent["owner_id"])

    yield OutboundAction(
        stripe_refund_contract,
        name="csv_refund",
        revision="v1",
        scope=scope,
        integration_id=os.environ.get(
            "GREGALE_STRIPE_INTEGRATION", "33333333-3333-4333-8333-333333333333"
        ),
        relay_url=relay_url,
        token=os.environ["FAAS_TOKEN"],
        store=PostgresOutboundActionStore(query),
        provider=StripeRefundActionProvider(
            account_id=os.environ.get("GREGALE_STRIPE_ACCOUNT", "acct_fixture"),
            api_version=os.environ.get(
                "GREGALE_STRIPE_API_VERSION", "2026-10-28.endive"
            ),
        ),
        tool_store=PostgresPipelineToolStore(query),
        gate_store=PostgresPipelineGateStore(query),
        review_base_url=review_base_url,
        required_roles=("reviewer",),
        authorize=authorize,
        authorize_execution=execution,
    )
