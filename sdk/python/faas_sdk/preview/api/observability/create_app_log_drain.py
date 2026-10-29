from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_log_drain_response import AppLogDrainResponse
from ...models.create_app_log_drain_request import CreateAppLogDrainRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateAppLogDrainRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/log-drains".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppLogDrainResponse | Problem | None:
    if response.status_code == 201:
        response_201 = AppLogDrainResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppLogDrainResponse | Problem]:
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
    body: CreateAppLogDrainRequest,
) -> Response[AppLogDrainResponse | Problem]:
    """Export runtime logs to an external HTTP or OTLP endpoint.

     The platform tails the existing per-instance runtime log ring and
    forwards each line through a bounded queue. `http_json` sends a
    provider-neutral JSON record; `otlp` sends an OTLP/HTTP JSON logs
    envelope suitable for a collector or vendor OTLP endpoint. The URL
    is SSRF-checked, and auth_header is sealed at rest and never echoed.

    Args:
        slug (str):
        body (CreateAppLogDrainRequest): Create a provider-neutral runtime log destination.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppLogDrainResponse | Problem]
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
    body: CreateAppLogDrainRequest,
) -> AppLogDrainResponse | Problem | None:
    """Export runtime logs to an external HTTP or OTLP endpoint.

     The platform tails the existing per-instance runtime log ring and
    forwards each line through a bounded queue. `http_json` sends a
    provider-neutral JSON record; `otlp` sends an OTLP/HTTP JSON logs
    envelope suitable for a collector or vendor OTLP endpoint. The URL
    is SSRF-checked, and auth_header is sealed at rest and never echoed.

    Args:
        slug (str):
        body (CreateAppLogDrainRequest): Create a provider-neutral runtime log destination.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppLogDrainResponse | Problem
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
    body: CreateAppLogDrainRequest,
) -> Response[AppLogDrainResponse | Problem]:
    """Export runtime logs to an external HTTP or OTLP endpoint.

     The platform tails the existing per-instance runtime log ring and
    forwards each line through a bounded queue. `http_json` sends a
    provider-neutral JSON record; `otlp` sends an OTLP/HTTP JSON logs
    envelope suitable for a collector or vendor OTLP endpoint. The URL
    is SSRF-checked, and auth_header is sealed at rest and never echoed.

    Args:
        slug (str):
        body (CreateAppLogDrainRequest): Create a provider-neutral runtime log destination.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppLogDrainResponse | Problem]
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
    body: CreateAppLogDrainRequest,
) -> AppLogDrainResponse | Problem | None:
    """Export runtime logs to an external HTTP or OTLP endpoint.

     The platform tails the existing per-instance runtime log ring and
    forwards each line through a bounded queue. `http_json` sends a
    provider-neutral JSON record; `otlp` sends an OTLP/HTTP JSON logs
    envelope suitable for a collector or vendor OTLP endpoint. The URL
    is SSRF-checked, and auth_header is sealed at rest and never echoed.

    Args:
        slug (str):
        body (CreateAppLogDrainRequest): Create a provider-neutral runtime log destination.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppLogDrainResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
