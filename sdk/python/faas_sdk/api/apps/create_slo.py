from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_slo_request import CreateSLORequest
from ...models.problem import Problem
from ...models.slo_response import SLOResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateSLORequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/slos".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | SLOResponse | None:
    if response.status_code == 201:
        response_201 = SLOResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | SLOResponse]:
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
    body: CreateSLORequest,
) -> Response[Problem | SLOResponse]:
    """Define an SLO on the app (ADR-747)

     Creates an availability or latency SLO with an objective between 90% and 99.99% over a rolling 7- or
    30-day window. Latency thresholds are limited to the gateway histogram bucket bounds so attainment
    is exact. An app can hold at most 10 SLOs.

    Args:
        slug (str):
        body (CreateSLORequest): Defines a customer SLO on an app (ADR-747).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SLOResponse]
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
    body: CreateSLORequest,
) -> Problem | SLOResponse | None:
    """Define an SLO on the app (ADR-747)

     Creates an availability or latency SLO with an objective between 90% and 99.99% over a rolling 7- or
    30-day window. Latency thresholds are limited to the gateway histogram bucket bounds so attainment
    is exact. An app can hold at most 10 SLOs.

    Args:
        slug (str):
        body (CreateSLORequest): Defines a customer SLO on an app (ADR-747).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SLOResponse
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
    body: CreateSLORequest,
) -> Response[Problem | SLOResponse]:
    """Define an SLO on the app (ADR-747)

     Creates an availability or latency SLO with an objective between 90% and 99.99% over a rolling 7- or
    30-day window. Latency thresholds are limited to the gateway histogram bucket bounds so attainment
    is exact. An app can hold at most 10 SLOs.

    Args:
        slug (str):
        body (CreateSLORequest): Defines a customer SLO on an app (ADR-747).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SLOResponse]
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
    body: CreateSLORequest,
) -> Problem | SLOResponse | None:
    """Define an SLO on the app (ADR-747)

     Creates an availability or latency SLO with an objective between 90% and 99.99% over a rolling 7- or
    30-day window. Latency thresholds are limited to the gateway histogram bucket bounds so attainment
    is exact. An app can hold at most 10 SLOs.

    Args:
        slug (str):
        body (CreateSLORequest): Defines a customer SLO on an app (ADR-747).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SLOResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
