from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.sales_inquiry_challenge_response_200 import SalesInquiryChallengeResponse200
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/sales/inquiries/challenge",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SalesInquiryChallengeResponse200 | None:
    if response.status_code == 200:
        response_200 = SalesInquiryChallengeResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | SalesInquiryChallengeResponse200]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | SalesInquiryChallengeResponse200]:
    """Prepare an anonymous website sales inquiry

     Requires the default-off sales intake flag. This opaque one-hour form token must be at least two
    seconds old when submitted. The challenge and submission share a separate ten-requests-per-minute IP
    budget.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SalesInquiryChallengeResponse200]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | SalesInquiryChallengeResponse200 | None:
    """Prepare an anonymous website sales inquiry

     Requires the default-off sales intake flag. This opaque one-hour form token must be at least two
    seconds old when submitted. The challenge and submission share a separate ten-requests-per-minute IP
    budget.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SalesInquiryChallengeResponse200
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | SalesInquiryChallengeResponse200]:
    """Prepare an anonymous website sales inquiry

     Requires the default-off sales intake flag. This opaque one-hour form token must be at least two
    seconds old when submitted. The challenge and submission share a separate ten-requests-per-minute IP
    budget.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SalesInquiryChallengeResponse200]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | SalesInquiryChallengeResponse200 | None:
    """Prepare an anonymous website sales inquiry

     Requires the default-off sales intake flag. This opaque one-hour form token must be at least two
    seconds old when submitted. The challenge and submission share a separate ten-requests-per-minute IP
    budget.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SalesInquiryChallengeResponse200
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
