from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_ai_request_content import OutboundAIRequestContent
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    integration: UUID,
    request: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/integrations/{integration}/ai-requests/{request}/content".format(
            integration=quote(str(integration), safe=""),
            request=quote(str(request), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAIRequestContent | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundAIRequestContent.from_dict(response.json())

        return response_200

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OutboundAIRequestContent | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    integration: UUID,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundAIRequestContent | Problem]:
    """Read one logged AI call's captured bodies.

     Requires MFA and read scope (ADR-996). Returns the start of the request and response bodies the
    gateway sent and received when request_log_content was on. Captures are kept for 3 days; a call
    without one is not found.

    Args:
        integration (UUID):
        request (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIRequestContent | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        request=request,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundAIRequestContent | Problem | None:
    """Read one logged AI call's captured bodies.

     Requires MFA and read scope (ADR-996). Returns the start of the request and response bodies the
    gateway sent and received when request_log_content was on. Captures are kept for 3 days; a call
    without one is not found.

    Args:
        integration (UUID):
        request (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIRequestContent | Problem
    """

    return sync_detailed(
        integration=integration,
        request=request,
        client=client,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundAIRequestContent | Problem]:
    """Read one logged AI call's captured bodies.

     Requires MFA and read scope (ADR-996). Returns the start of the request and response bodies the
    gateway sent and received when request_log_content was on. Captures are kept for 3 days; a call
    without one is not found.

    Args:
        integration (UUID):
        request (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIRequestContent | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        request=request,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundAIRequestContent | Problem | None:
    """Read one logged AI call's captured bodies.

     Requires MFA and read scope (ADR-996). Returns the start of the request and response bodies the
    gateway sent and received when request_log_content was on. Captures are kept for 3 days; a call
    without one is not found.

    Args:
        integration (UUID):
        request (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIRequestContent | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            request=request,
            client=client,
        )
    ).parsed
