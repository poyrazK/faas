from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_message_mutation_request import ManagedRealtimeMessageMutationRequest
from ...models.managed_realtime_message_mutation_response import ManagedRealtimeMessageMutationResponse
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    body: ManagedRealtimeMessageMutationRequest,
    principal: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["principal"] = principal

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/{message_id}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            message_id=quote(str(message_id), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeMessageMutationResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeMessageMutationResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedRealtimeMessageMutationResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeMessageMutationRequest,
    principal: str,
) -> Response[ManagedRealtimeMessageMutationResponse | Problem]:
    """Update a retained inbox message

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        body (ManagedRealtimeMessageMutationRequest): Expected message version and replacement
            payload for a retained event.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeMessageMutationResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        message_id=message_id,
        body=body,
        principal=principal,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeMessageMutationRequest,
    principal: str,
) -> ManagedRealtimeMessageMutationResponse | Problem | None:
    """Update a retained inbox message

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        body (ManagedRealtimeMessageMutationRequest): Expected message version and replacement
            payload for a retained event.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeMessageMutationResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        message_id=message_id,
        client=client,
        body=body,
        principal=principal,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeMessageMutationRequest,
    principal: str,
) -> Response[ManagedRealtimeMessageMutationResponse | Problem]:
    """Update a retained inbox message

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        body (ManagedRealtimeMessageMutationRequest): Expected message version and replacement
            payload for a retained event.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeMessageMutationResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        message_id=message_id,
        body=body,
        principal=principal,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    message_id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeMessageMutationRequest,
    principal: str,
) -> ManagedRealtimeMessageMutationResponse | Problem | None:
    """Update a retained inbox message

    Args:
        slug (str):
        id (UUID):
        message_id (str):
        principal (str):
        body (ManagedRealtimeMessageMutationRequest): Expected message version and replacement
            payload for a retained event.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeMessageMutationResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            message_id=message_id,
            client=client,
            body=body,
            principal=principal,
        )
    ).parsed
