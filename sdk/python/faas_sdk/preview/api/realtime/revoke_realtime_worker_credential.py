from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    channel: str,
    group: str,
    credential_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/revoke".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            channel=quote(str(channel), safe=""),
            group=quote(str(group), safe=""),
            credential_id=quote(str(credential_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    credential_id: str,
    *,
    client: AuthenticatedClient,
) -> Response[Any | Problem]:
    """Revoke a worker credential

     Revocation is available even when the endpoint or history preview is disabled. Once committed, later
    operations fail; already-authorized transactions can finish before revocation obtains the group
    lock. Revoked credentials cannot be rotated.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        credential_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        credential_id=credential_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    channel: str,
    group: str,
    credential_id: str,
    *,
    client: AuthenticatedClient,
) -> Any | Problem | None:
    """Revoke a worker credential

     Revocation is available even when the endpoint or history preview is disabled. Once committed, later
    operations fail; already-authorized transactions can finish before revocation obtains the group
    lock. Revoked credentials cannot be rotated.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        credential_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        credential_id=credential_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    channel: str,
    group: str,
    credential_id: str,
    *,
    client: AuthenticatedClient,
) -> Response[Any | Problem]:
    """Revoke a worker credential

     Revocation is available even when the endpoint or history preview is disabled. Once committed, later
    operations fail; already-authorized transactions can finish before revocation obtains the group
    lock. Revoked credentials cannot be rotated.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        credential_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        channel=channel,
        group=group,
        credential_id=credential_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    channel: str,
    group: str,
    credential_id: str,
    *,
    client: AuthenticatedClient,
) -> Any | Problem | None:
    """Revoke a worker credential

     Revocation is available even when the endpoint or history preview is disabled. Once committed, later
    operations fail; already-authorized transactions can finish before revocation obtains the group
    lock. Revoked credentials cannot be rotated.

    Args:
        slug (str):
        id (str):
        channel (str):
        group (str):
        credential_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            channel=channel,
            group=group,
            credential_id=credential_id,
            client=client,
        )
    ).parsed
