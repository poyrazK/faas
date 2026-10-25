from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_billing_policy_response import APIConsumerBillingPolicyResponse
from ...models.problem import Problem
from ...models.put_api_consumer_billing_policy_request import PutAPIConsumerBillingPolicyRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: PutAPIConsumerBillingPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/consumer-billing-policy".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerBillingPolicyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerBillingPolicyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIConsumerBillingPolicyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerBillingPolicyRequest,
) -> Response[APIConsumerBillingPolicyResponse | Problem]:
    """Refuse consumers whose exported invoice is unpaid some days after its due date.

     Every 5 minutes apid places a hold on each consumer whose exported
    invoice is open or uncollectible with an amount left at least
    block_overdue_after_days past its due date, and lifts holds that no
    longer apply. The gateway answers a held consumer with 402
    `consumer_invoice_overdue`.

    Args:
        slug (str):
        body (PutAPIConsumerBillingPolicyRequest): An app's overdue-invoice rule (ADR-1315).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerBillingPolicyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerBillingPolicyRequest,
) -> APIConsumerBillingPolicyResponse | Problem | None:
    """Refuse consumers whose exported invoice is unpaid some days after its due date.

     Every 5 minutes apid places a hold on each consumer whose exported
    invoice is open or uncollectible with an amount left at least
    block_overdue_after_days past its due date, and lifts holds that no
    longer apply. The gateway answers a held consumer with 402
    `consumer_invoice_overdue`.

    Args:
        slug (str):
        body (PutAPIConsumerBillingPolicyRequest): An app's overdue-invoice rule (ADR-1315).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerBillingPolicyResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerBillingPolicyRequest,
) -> Response[APIConsumerBillingPolicyResponse | Problem]:
    """Refuse consumers whose exported invoice is unpaid some days after its due date.

     Every 5 minutes apid places a hold on each consumer whose exported
    invoice is open or uncollectible with an amount left at least
    block_overdue_after_days past its due date, and lifts holds that no
    longer apply. The gateway answers a held consumer with 402
    `consumer_invoice_overdue`.

    Args:
        slug (str):
        body (PutAPIConsumerBillingPolicyRequest): An app's overdue-invoice rule (ADR-1315).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerBillingPolicyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerBillingPolicyRequest,
) -> APIConsumerBillingPolicyResponse | Problem | None:
    """Refuse consumers whose exported invoice is unpaid some days after its due date.

     Every 5 minutes apid places a hold on each consumer whose exported
    invoice is open or uncollectible with an amount left at least
    block_overdue_after_days past its due date, and lifts holds that no
    longer apply. The gateway answers a held consumer with 402
    `consumer_invoice_overdue`.

    Args:
        slug (str):
        body (PutAPIConsumerBillingPolicyRequest): An app's overdue-invoice rule (ADR-1315).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerBillingPolicyResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
