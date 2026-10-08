from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.unregister_managed_realtime_push_device_response_200 import (
    UnregisterManagedRealtimePushDeviceResponse200,
)
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    id: str,
    device: str,
    *,
    principal: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["principal"] = principal

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/push/devices/{device}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            device=quote(str(device), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | UnregisterManagedRealtimePushDeviceResponse200 | None:
    if response.status_code == 200:
        response_200 = UnregisterManagedRealtimePushDeviceResponse200.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | UnregisterManagedRealtimePushDeviceResponse200]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    device: str,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
) -> Response[Problem | UnregisterManagedRealtimePushDeviceResponse200]:
    """Remove a push registration and cancel its queued work.

     Requires the retained-history preview gate. Registration is limited to 16 devices per principal and
    1024 per endpoint. A provider must be enabled before registration. Identical registrations preserve
    pending deliveries; changed tokens cancel old work.

    Args:
        slug (str):
        id (str):
        device (str):
        principal (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | UnregisterManagedRealtimePushDeviceResponse200]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        device=device,
        principal=principal,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    device: str,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
) -> Problem | UnregisterManagedRealtimePushDeviceResponse200 | None:
    """Remove a push registration and cancel its queued work.

     Requires the retained-history preview gate. Registration is limited to 16 devices per principal and
    1024 per endpoint. A provider must be enabled before registration. Identical registrations preserve
    pending deliveries; changed tokens cancel old work.

    Args:
        slug (str):
        id (str):
        device (str):
        principal (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | UnregisterManagedRealtimePushDeviceResponse200
    """

    return sync_detailed(
        slug=slug,
        id=id,
        device=device,
        client=client,
        principal=principal,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    device: str,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
) -> Response[Problem | UnregisterManagedRealtimePushDeviceResponse200]:
    """Remove a push registration and cancel its queued work.

     Requires the retained-history preview gate. Registration is limited to 16 devices per principal and
    1024 per endpoint. A provider must be enabled before registration. Identical registrations preserve
    pending deliveries; changed tokens cancel old work.

    Args:
        slug (str):
        id (str):
        device (str):
        principal (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | UnregisterManagedRealtimePushDeviceResponse200]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        device=device,
        principal=principal,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    device: str,
    *,
    client: AuthenticatedClient | Client,
    principal: str,
) -> Problem | UnregisterManagedRealtimePushDeviceResponse200 | None:
    """Remove a push registration and cancel its queued work.

     Requires the retained-history preview gate. Registration is limited to 16 devices per principal and
    1024 per endpoint. A provider must be enabled before registration. Identical registrations preserve
    pending deliveries; changed tokens cancel old work.

    Args:
        slug (str):
        id (str):
        device (str):
        principal (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | UnregisterManagedRealtimePushDeviceResponse200
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            device=device,
            client=client,
            principal=principal,
        )
    ).parsed
