from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.route_policy_receipt import RoutePolicyReceipt
from ...types import Response


def _get_kwargs(
    slug: str,
    receipt_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-policy/receipts/{receipt_id}".format(
            slug=quote(str(slug), safe=""),
            receipt_id=quote(str(receipt_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RoutePolicyReceipt | None:
    if response.status_code == 200:
        response_200 = RoutePolicyReceipt.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RoutePolicyReceipt]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    receipt_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RoutePolicyReceipt]:
    """get route policyReceipt.

     Recover the committed rule IDs and configuration verification for this owned app. The receipt is
    historical evidence, not a fresh live policy or gateway check.

    Args:
        slug (str):
        receipt_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RoutePolicyReceipt]
    """

    kwargs = _get_kwargs(
        slug=slug,
        receipt_id=receipt_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    receipt_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RoutePolicyReceipt | None:
    """get route policyReceipt.

     Recover the committed rule IDs and configuration verification for this owned app. The receipt is
    historical evidence, not a fresh live policy or gateway check.

    Args:
        slug (str):
        receipt_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RoutePolicyReceipt
    """

    return sync_detailed(
        slug=slug,
        receipt_id=receipt_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    receipt_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RoutePolicyReceipt]:
    """get route policyReceipt.

     Recover the committed rule IDs and configuration verification for this owned app. The receipt is
    historical evidence, not a fresh live policy or gateway check.

    Args:
        slug (str):
        receipt_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RoutePolicyReceipt]
    """

    kwargs = _get_kwargs(
        slug=slug,
        receipt_id=receipt_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    receipt_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RoutePolicyReceipt | None:
    """get route policyReceipt.

     Recover the committed rule IDs and configuration verification for this owned app. The receipt is
    historical evidence, not a fresh live policy or gateway check.

    Args:
        slug (str):
        receipt_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RoutePolicyReceipt
    """

    return (
        await asyncio_detailed(
            slug=slug,
            receipt_id=receipt_id,
            client=client,
        )
    ).parsed
