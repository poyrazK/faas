"""Private fixture pipe; not a public webhook handler or authentication mechanism."""

from __future__ import annotations

import hmac
import json
import os
from dataclasses import asdict

import psycopg
from faas_sdk.preview import (
    PipelineScope,
    PsycopgEventOutboxPool,
    StripeWebhookDelivery,
    stripe_delivery_context_from_headers,
)
from paid_producer import paid_csv_adapter
from psycopg.rows import dict_row


async def execute(request):
    scope = PipelineScope.from_dict(json.loads(os.environ["GREGALE_PIPELINE_SCOPE"]))

    async def connect():
        return await psycopg.AsyncConnection.connect(
            os.environ["DATABASE_URL"], autocommit=True, row_factory=dict_row
        )

    async def verify(delivery):
        if not hmac.compare_digest(
            delivery.headers.get("authorization", ""),
            "Bearer " + os.environ["GREGALE_CSV_STRIPE_KEY"],
        ):
            raise ValueError("unauthorized")
        return stripe_delivery_context_from_headers(
            delivery.headers, delivery.body, scope.account_id, scope.app_id
        )

    adapter = paid_csv_adapter(
        pool=PsycopgEventOutboxPool(connect),
        scope=scope,
        endpoint_id=request["endpoint_id"],
        token=os.environ["FAAS_TOKEN"],
        verify=verify,
    )
    d = request["delivery"]
    delivery = StripeWebhookDelivery(
        method="POST", path=d["path"], headers=d["headers"], body=d["body"].encode()
    )
    result = await (
        adapter.inspect(delivery, request["access"], timeout=10)
        if request["action"] == "stripe-inspect"
        else adapter.handle(delivery, request["access"], timeout=10)
    )
    return asdict(result) if result is not None else None
