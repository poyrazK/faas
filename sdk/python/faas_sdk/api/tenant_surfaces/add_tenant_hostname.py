from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.add_tenant_hostname_request import AddTenantHostnameRequest
from ...models.problem import Problem
from ...models.tenant_hostname_response import TenantHostnameResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: AddTenantHostnameRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/tenant-surfaces/{id}/hostnames".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | TenantHostnameResponse | None:
    if response.status_code == 202:
        response_202 = TenantHostnameResponse.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | TenantHostnameResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: AddTenantHostnameRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | TenantHostnameResponse]:
    """Add a hostname to an existing surface.

    Args:
        slug (str):
        id (str):
        idempotency_key (str | Unset):
        body (AddTenantHostnameRequest): Append a hostname to an existing surface.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TenantHostnameResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: AddTenantHostnameRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | TenantHostnameResponse | None:
    """Add a hostname to an existing surface.

    Args:
        slug (str):
        id (str):
        idempotency_key (str | Unset):
        body (AddTenantHostnameRequest): Append a hostname to an existing surface.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TenantHostnameResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: AddTenantHostnameRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | TenantHostnameResponse]:
    """Add a hostname to an existing surface.

    Args:
        slug (str):
        id (str):
        idempotency_key (str | Unset):
        body (AddTenantHostnameRequest): Append a hostname to an existing surface.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TenantHostnameResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: AddTenantHostnameRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | TenantHostnameResponse | None:
    """Add a hostname to an existing surface.

    Args:
        slug (str):
        id (str):
        idempotency_key (str | Unset):
        body (AddTenantHostnameRequest): Append a hostname to an existing surface.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TenantHostnameResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
