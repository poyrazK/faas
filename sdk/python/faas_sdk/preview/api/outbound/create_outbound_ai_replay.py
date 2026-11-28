from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_outbound_ai_replay_request import CreateOutboundAIReplayRequest
from ...models.outbound_ai_replay import OutboundAIReplay
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    integration: UUID,
    request: UUID,
    *,
    body: CreateOutboundAIReplayRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/outbound/integrations/{integration}/ai-requests/{request}/replays".format(
            integration=quote(str(integration), safe=""),
            request=quote(str(request), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAIReplay | Problem | None:
    if response.status_code == 202:
        response_202 = OutboundAIReplay.from_dict(response.json())

        return response_202

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OutboundAIReplay | Problem]:
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
    body: CreateOutboundAIReplayRequest,
) -> Response[OutboundAIReplay | Problem]:
    """Replay a captured AI call on other integrations or models.

     Requires MFA and deploy-write scope. The outbound gateway resends the captured request to each
    target without streaming; the calls count against the targets' budgets. At most 100 replays per
    integration per UTC day.

    Args:
        integration (UUID):
        request (UUID):
        body (CreateOutboundAIReplayRequest): Replay a captured call on up to 5 targets.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIReplay | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        request=request,
        body=body,
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
    body: CreateOutboundAIReplayRequest,
) -> OutboundAIReplay | Problem | None:
    """Replay a captured AI call on other integrations or models.

     Requires MFA and deploy-write scope. The outbound gateway resends the captured request to each
    target without streaming; the calls count against the targets' budgets. At most 100 replays per
    integration per UTC day.

    Args:
        integration (UUID):
        request (UUID):
        body (CreateOutboundAIReplayRequest): Replay a captured call on up to 5 targets.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIReplay | Problem
    """

    return sync_detailed(
        integration=integration,
        request=request,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIReplayRequest,
) -> Response[OutboundAIReplay | Problem]:
    """Replay a captured AI call on other integrations or models.

     Requires MFA and deploy-write scope. The outbound gateway resends the captured request to each
    target without streaming; the calls count against the targets' budgets. At most 100 replays per
    integration per UTC day.

    Args:
        integration (UUID):
        request (UUID):
        body (CreateOutboundAIReplayRequest): Replay a captured call on up to 5 targets.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAIReplay | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        request=request,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    request: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOutboundAIReplayRequest,
) -> OutboundAIReplay | Problem | None:
    """Replay a captured AI call on other integrations or models.

     Requires MFA and deploy-write scope. The outbound gateway resends the captured request to each
    target without streaming; the calls count against the targets' budgets. At most 100 replays per
    integration per UTC day.

    Args:
        integration (UUID):
        request (UUID):
        body (CreateOutboundAIReplayRequest): Replay a captured call on up to 5 targets.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAIReplay | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            request=request,
            client=client,
            body=body,
        )
    ).parsed
