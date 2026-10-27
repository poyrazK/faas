from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_access_response import ManagedRealtimeAccessResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    principal: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            principal=quote(str(principal), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeAccessResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeAccessResponse.from_dict(response.json())

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
) -> Response[ManagedRealtimeAccessResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    principal: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedRealtimeAccessResponse | Problem]:
    """Read durable channel denies and session cutoff for one principal.

     Requires MFA and read scope. Operator preview requires FAAS_REALTIME_ACCESS_PREVIEW_ENABLED=1 on the
    API and every realtime node. OIDC endpoints only. Revision zero means no stored controls. Responses
    contain no token or connection identifiers.

    Args:
        slug (str):
        id (str):
        principal (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeAccessResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        principal=principal,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    principal: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedRealtimeAccessResponse | Problem | None:
    """Read durable channel denies and session cutoff for one principal.

     Requires MFA and read scope. Operator preview requires FAAS_REALTIME_ACCESS_PREVIEW_ENABLED=1 on the
    API and every realtime node. OIDC endpoints only. Revision zero means no stored controls. Responses
    contain no token or connection identifiers.

    Args:
        slug (str):
        id (str):
        principal (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeAccessResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        principal=principal,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    principal: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ManagedRealtimeAccessResponse | Problem]:
    """Read durable channel denies and session cutoff for one principal.

     Requires MFA and read scope. Operator preview requires FAAS_REALTIME_ACCESS_PREVIEW_ENABLED=1 on the
    API and every realtime node. OIDC endpoints only. Revision zero means no stored controls. Responses
    contain no token or connection identifiers.

    Args:
        slug (str):
        id (str):
        principal (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeAccessResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        principal=principal,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    principal: str,
    *,
    client: AuthenticatedClient | Client,
) -> ManagedRealtimeAccessResponse | Problem | None:
    """Read durable channel denies and session cutoff for one principal.

     Requires MFA and read scope. Operator preview requires FAAS_REALTIME_ACCESS_PREVIEW_ENABLED=1 on the
    API and every realtime node. OIDC endpoints only. Revision zero means no stored controls. Responses
    contain no token or connection identifiers.

    Args:
        slug (str):
        id (str):
        principal (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeAccessResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            principal=principal,
            client=client,
        )
    ).parsed
