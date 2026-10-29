from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_spend_cap_response import APIConsumerSpendCapResponse
from ...models.problem import Problem
from ...models.put_api_consumer_spend_cap_request import PutAPIConsumerSpendCapRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
    *,
    body: PutAPIConsumerSpendCapRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/spend-cap".format(
            slug=quote(str(slug), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerSpendCapResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerSpendCapResponse.from_dict(response.json())

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
) -> Response[APIConsumerSpendCapResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerSpendCapRequest,
) -> Response[APIConsumerSpendCapResponse | Problem]:
    """Set a consumer's monthly spend cap.

     Once the month's amount due passes the cap, the gateway answers the
    consumer with 402 `consumer_spend_cap_reached` until the month ends,
    the cap rises or credit is added. The amount due is the month to
    date priced like a statement, minus usable prepaid credit. The API
    checks it every minute and at once when the cap or credit changes,
    so a consumer can overshoot by about two minutes of traffic. A zero
    cap allows prepaid usage only.

    Args:
        slug (str):
        consumer_id (UUID):
        body (PutAPIConsumerSpendCapRequest): A consumer's monthly spend cap after prepaid credit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerSpendCapResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerSpendCapRequest,
) -> APIConsumerSpendCapResponse | Problem | None:
    """Set a consumer's monthly spend cap.

     Once the month's amount due passes the cap, the gateway answers the
    consumer with 402 `consumer_spend_cap_reached` until the month ends,
    the cap rises or credit is added. The amount due is the month to
    date priced like a statement, minus usable prepaid credit. The API
    checks it every minute and at once when the cap or credit changes,
    so a consumer can overshoot by about two minutes of traffic. A zero
    cap allows prepaid usage only.

    Args:
        slug (str):
        consumer_id (UUID):
        body (PutAPIConsumerSpendCapRequest): A consumer's monthly spend cap after prepaid credit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerSpendCapResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        consumer_id=consumer_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerSpendCapRequest,
) -> Response[APIConsumerSpendCapResponse | Problem]:
    """Set a consumer's monthly spend cap.

     Once the month's amount due passes the cap, the gateway answers the
    consumer with 402 `consumer_spend_cap_reached` until the month ends,
    the cap rises or credit is added. The amount due is the month to
    date priced like a statement, minus usable prepaid credit. The API
    checks it every minute and at once when the cap or credit changes,
    so a consumer can overshoot by about two minutes of traffic. A zero
    cap allows prepaid usage only.

    Args:
        slug (str):
        consumer_id (UUID):
        body (PutAPIConsumerSpendCapRequest): A consumer's monthly spend cap after prepaid credit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerSpendCapResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutAPIConsumerSpendCapRequest,
) -> APIConsumerSpendCapResponse | Problem | None:
    """Set a consumer's monthly spend cap.

     Once the month's amount due passes the cap, the gateway answers the
    consumer with 402 `consumer_spend_cap_reached` until the month ends,
    the cap rises or credit is added. The amount due is the month to
    date priced like a statement, minus usable prepaid credit. The API
    checks it every minute and at once when the cap or credit changes,
    so a consumer can overshoot by about two minutes of traffic. A zero
    cap allows prepaid usage only.

    Args:
        slug (str):
        consumer_id (UUID):
        body (PutAPIConsumerSpendCapRequest): A consumer's monthly spend cap after prepaid credit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerSpendCapResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            consumer_id=consumer_id,
            client=client,
            body=body,
        )
    ).parsed
