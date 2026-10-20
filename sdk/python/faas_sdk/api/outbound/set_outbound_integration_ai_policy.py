from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.put_outbound_ai_policy_request import PutOutboundAIPolicyRequest
from ...types import Response


def _get_kwargs(
    integration: UUID,
    *,
    body: PutOutboundAIPolicyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/outbound/integrations/{integration}/ai".format(
            integration=quote(str(integration), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIPolicyRequest,
) -> Response[Any | Problem]:
    """Set or clear a customer-owned integration's AI provider and daily token limit.

     Requires MFA and deploy-write scope (ADR-988). The gateway extracts token usage from responses in
    the selected provider's format and stops new calls once the UTC day's settled input plus output
    tokens reach the limit; calls already in flight may finish above it.

    Args:
        integration (UUID):
        body (PutOutboundAIPolicyRequest): Replace an integration's AI gateway settings. An empty
            ai_provider turns AI handling off and requires a null daily_token_limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIPolicyRequest,
) -> Any | Problem | None:
    """Set or clear a customer-owned integration's AI provider and daily token limit.

     Requires MFA and deploy-write scope (ADR-988). The gateway extracts token usage from responses in
    the selected provider's format and stops new calls once the UTC day's settled input plus output
    tokens reach the limit; calls already in flight may finish above it.

    Args:
        integration (UUID):
        body (PutOutboundAIPolicyRequest): Replace an integration's AI gateway settings. An empty
            ai_provider turns AI handling off and requires a null daily_token_limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIPolicyRequest,
) -> Response[Any | Problem]:
    """Set or clear a customer-owned integration's AI provider and daily token limit.

     Requires MFA and deploy-write scope (ADR-988). The gateway extracts token usage from responses in
    the selected provider's format and stops new calls once the UTC day's settled input plus output
    tokens reach the limit; calls already in flight may finish above it.

    Args:
        integration (UUID):
        body (PutOutboundAIPolicyRequest): Replace an integration's AI gateway settings. An empty
            ai_provider turns AI handling off and requires a null daily_token_limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutOutboundAIPolicyRequest,
) -> Any | Problem | None:
    """Set or clear a customer-owned integration's AI provider and daily token limit.

     Requires MFA and deploy-write scope (ADR-988). The gateway extracts token usage from responses in
    the selected provider's format and stops new calls once the UTC day's settled input plus output
    tokens reach the limit; calls already in flight may finish above it.

    Args:
        integration (UUID):
        body (PutOutboundAIPolicyRequest): Replace an integration's AI gateway settings. An empty
            ai_provider turns AI handling off and requires a null daily_token_limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
            body=body,
        )
    ).parsed
