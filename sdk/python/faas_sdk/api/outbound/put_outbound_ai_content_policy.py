from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_ai_content_policy import OutboundAIContentPolicy
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    integration: UUID,
    *,
    body: OutboundAIContentPolicy,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/outbound/integrations/{integration}/ai-content-policy".format(
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
    body: OutboundAIContentPolicy,
) -> Response[Any | Problem]:
    """Replace an AI integration's content guardrails.

     Requires MFA and deploy-write scope (ADR-997). An active policy requires an AI provider; with one,
    request bodies the gateway cannot read are refused.

    Args:
        integration (UUID):
        body (OutboundAIContentPolicy): An integration's content guardrails (ADR-997). Each
            detector is off, redacts its findings before the request reaches the provider, or blocks
            the request. A request containing a blocked term, compared without case, is blocked. The
            gateway checks the string values of JSON bodies and the text fields of forms.

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
    body: OutboundAIContentPolicy,
) -> Any | Problem | None:
    """Replace an AI integration's content guardrails.

     Requires MFA and deploy-write scope (ADR-997). An active policy requires an AI provider; with one,
    request bodies the gateway cannot read are refused.

    Args:
        integration (UUID):
        body (OutboundAIContentPolicy): An integration's content guardrails (ADR-997). Each
            detector is off, redacts its findings before the request reaches the provider, or blocks
            the request. A request containing a blocked term, compared without case, is blocked. The
            gateway checks the string values of JSON bodies and the text fields of forms.

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
    body: OutboundAIContentPolicy,
) -> Response[Any | Problem]:
    """Replace an AI integration's content guardrails.

     Requires MFA and deploy-write scope (ADR-997). An active policy requires an AI provider; with one,
    request bodies the gateway cannot read are refused.

    Args:
        integration (UUID):
        body (OutboundAIContentPolicy): An integration's content guardrails (ADR-997). Each
            detector is off, redacts its findings before the request reaches the provider, or blocks
            the request. A request containing a blocked term, compared without case, is blocked. The
            gateway checks the string values of JSON bodies and the text fields of forms.

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
    body: OutboundAIContentPolicy,
) -> Any | Problem | None:
    """Replace an AI integration's content guardrails.

     Requires MFA and deploy-write scope (ADR-997). An active policy requires an AI provider; with one,
    request bodies the gateway cannot read are refused.

    Args:
        integration (UUID):
        body (OutboundAIContentPolicy): An integration's content guardrails (ADR-997). Each
            detector is off, redacts its findings before the request reaches the provider, or blocks
            the request. A request containing a blocked term, compared without case, is blocked. The
            gateway checks the string values of JSON bodies and the text fields of forms.

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
