from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_outbound_ai_route_request import CreateOutboundAIRouteRequest
from ...models.outbound_ai_route import OutboundAIRoute
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: CreateOutboundAIRouteRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/outbound/ai-routes",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | OutboundAIRoute | Problem | None:
    if response.status_code == 201:
        response_201 = OutboundAIRoute.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = cast(Any, None)
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
) -> Response[Any | OutboundAIRoute | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIRouteRequest,
) -> Response[Any | OutboundAIRoute | Problem]:
    """Create an AI fallback route over managed AI integrations.

     Requires MFA and deploy-write scope (ADR-990, ADR-991). Every target must be an enabled managed AI
    integration of this account; a native route needs one ai_provider, an openai route translates OpenAI
    chat completions for anthropic and gemini targets. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider
    path}; the gateway tries targets in order and moves on after a budget or availability rejection, a
    provider 408, 429 or 5xx, or a transport error, before any response byte is sent.

    Args:
        body (CreateOutboundAIRouteRequest): A new AI route with a DNS-label name, an optional
            format and 1 to 5 ordered targets.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OutboundAIRoute | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIRouteRequest,
) -> Any | OutboundAIRoute | Problem | None:
    """Create an AI fallback route over managed AI integrations.

     Requires MFA and deploy-write scope (ADR-990, ADR-991). Every target must be an enabled managed AI
    integration of this account; a native route needs one ai_provider, an openai route translates OpenAI
    chat completions for anthropic and gemini targets. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider
    path}; the gateway tries targets in order and moves on after a budget or availability rejection, a
    provider 408, 429 or 5xx, or a transport error, before any response byte is sent.

    Args:
        body (CreateOutboundAIRouteRequest): A new AI route with a DNS-label name, an optional
            format and 1 to 5 ordered targets.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OutboundAIRoute | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIRouteRequest,
) -> Response[Any | OutboundAIRoute | Problem]:
    """Create an AI fallback route over managed AI integrations.

     Requires MFA and deploy-write scope (ADR-990, ADR-991). Every target must be an enabled managed AI
    integration of this account; a native route needs one ai_provider, an openai route translates OpenAI
    chat completions for anthropic and gemini targets. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider
    path}; the gateway tries targets in order and moves on after a budget or availability rejection, a
    provider 408, 429 or 5xx, or a transport error, before any response byte is sent.

    Args:
        body (CreateOutboundAIRouteRequest): A new AI route with a DNS-label name, an optional
            format and 1 to 5 ordered targets.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OutboundAIRoute | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIRouteRequest,
) -> Any | OutboundAIRoute | Problem | None:
    """Create an AI fallback route over managed AI integrations.

     Requires MFA and deploy-write scope (ADR-990, ADR-991). Every target must be an enabled managed AI
    integration of this account; a native route needs one ai_provider, an openai route translates OpenAI
    chat completions for anthropic and gemini targets. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider
    path}; the gateway tries targets in order and moves on after a budget or availability rejection, a
    provider 408, 429 or 5xx, or a transport error, before any response byte is sent.

    Args:
        body (CreateOutboundAIRouteRequest): A new AI route with a DNS-label name, an optional
            format and 1 to 5 ordered targets.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OutboundAIRoute | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
