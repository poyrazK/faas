from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_recovery_job import EventRecoveryJob
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    request_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/event-recoveries/by-request/{request_id}".format(
            slug=quote(str(slug), safe=""),
            request_id=quote(str(request_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventRecoveryJob | Problem | None:
    if response.status_code == 200:
        response_200 = EventRecoveryJob.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventRecoveryJob | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[EventRecoveryJob | Problem]:
    """Find a retained recovery job by its stable request identifier

     Looks up the original recovery job in this app and account after an interrupted creation request.
    Missing or expired jobs return 404. This operation never creates work.

    Args:
        slug (str):
        request_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryJob | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request_id=request_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventRecoveryJob | Problem | None:
    """Find a retained recovery job by its stable request identifier

     Looks up the original recovery job in this app and account after an interrupted creation request.
    Missing or expired jobs return 404. This operation never creates work.

    Args:
        slug (str):
        request_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryJob | Problem
    """

    return sync_detailed(
        slug=slug,
        request_id=request_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[EventRecoveryJob | Problem]:
    """Find a retained recovery job by its stable request identifier

     Looks up the original recovery job in this app and account after an interrupted creation request.
    Missing or expired jobs return 404. This operation never creates work.

    Args:
        slug (str):
        request_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventRecoveryJob | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        request_id=request_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    request_id: UUID,
    *,
    client: AuthenticatedClient,
) -> EventRecoveryJob | Problem | None:
    """Find a retained recovery job by its stable request identifier

     Looks up the original recovery job in this app and account after an interrupted creation request.
    Missing or expired jobs return 404. This operation never creates work.

    Args:
        slug (str):
        request_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventRecoveryJob | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            request_id=request_id,
            client=client,
        )
    ).parsed
