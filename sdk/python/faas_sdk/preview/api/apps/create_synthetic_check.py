from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_synthetic_check_request import CreateSyntheticCheckRequest
from ...models.problem import Problem
from ...models.synthetic_check_response import SyntheticCheckResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateSyntheticCheckRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/synthetics".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | SyntheticCheckResponse | None:
    if response.status_code == 201:
        response_201 = SyntheticCheckResponse.from_dict(response.json())

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
) -> Response[Problem | SyntheticCheckResponse]:
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
    body: CreateSyntheticCheckRequest,
) -> Response[Problem | SyntheticCheckResponse]:
    """Define a synthetic check on the app (ADR-748)

     Schedules a GET or HEAD request to a path on the app's own hostname every 5, 15, or 60 minutes,
    through the public edge. A probe that wakes a parked app is billed like any request. An app can have
    at most 5 checks.

    Args:
        slug (str):
        body (CreateSyntheticCheckRequest): Defines a scheduled HTTP check against the app
            (ADR-748).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SyntheticCheckResponse]
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
    body: CreateSyntheticCheckRequest,
) -> Problem | SyntheticCheckResponse | None:
    """Define a synthetic check on the app (ADR-748)

     Schedules a GET or HEAD request to a path on the app's own hostname every 5, 15, or 60 minutes,
    through the public edge. A probe that wakes a parked app is billed like any request. An app can have
    at most 5 checks.

    Args:
        slug (str):
        body (CreateSyntheticCheckRequest): Defines a scheduled HTTP check against the app
            (ADR-748).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SyntheticCheckResponse
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
    body: CreateSyntheticCheckRequest,
) -> Response[Problem | SyntheticCheckResponse]:
    """Define a synthetic check on the app (ADR-748)

     Schedules a GET or HEAD request to a path on the app's own hostname every 5, 15, or 60 minutes,
    through the public edge. A probe that wakes a parked app is billed like any request. An app can have
    at most 5 checks.

    Args:
        slug (str):
        body (CreateSyntheticCheckRequest): Defines a scheduled HTTP check against the app
            (ADR-748).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | SyntheticCheckResponse]
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
    body: CreateSyntheticCheckRequest,
) -> Problem | SyntheticCheckResponse | None:
    """Define a synthetic check on the app (ADR-748)

     Schedules a GET or HEAD request to a path on the app's own hostname every 5, 15, or 60 minutes,
    through the public edge. A probe that wakes a parked app is billed like any request. An app can have
    at most 5 checks.

    Args:
        slug (str):
        body (CreateSyntheticCheckRequest): Defines a scheduled HTTP check against the app
            (ADR-748).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | SyntheticCheckResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
