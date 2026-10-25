from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_executions_response import OperationExecutionsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    after: int | Unset = 0,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["after"] = after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/operations/{id}/executions".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationExecutionsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationExecutionsResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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
) -> Response[OperationExecutionsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: int | Unset = 0,
    limit: int | Unset = 20,
) -> Response[OperationExecutionsResponse | Problem]:
    """Inspect retained operation execution generations.

     Requires account read scope and MFA. Rows are ordered by generation with attempt counts from the
    execution ledger. Payloads, headers and capabilities are omitted. Next_generation is the after
    watermark for the next page.

    Args:
        slug (str):
        id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationExecutionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        after=after,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: int | Unset = 0,
    limit: int | Unset = 20,
) -> OperationExecutionsResponse | Problem | None:
    """Inspect retained operation execution generations.

     Requires account read scope and MFA. Rows are ordered by generation with attempt counts from the
    execution ledger. Payloads, headers and capabilities are omitted. Next_generation is the after
    watermark for the next page.

    Args:
        slug (str):
        id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationExecutionsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: int | Unset = 0,
    limit: int | Unset = 20,
) -> Response[OperationExecutionsResponse | Problem]:
    """Inspect retained operation execution generations.

     Requires account read scope and MFA. Rows are ordered by generation with attempt counts from the
    execution ledger. Payloads, headers and capabilities are omitted. Next_generation is the after
    watermark for the next page.

    Args:
        slug (str):
        id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationExecutionsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: int | Unset = 0,
    limit: int | Unset = 20,
) -> OperationExecutionsResponse | Problem | None:
    """Inspect retained operation execution generations.

     Requires account read scope and MFA. Rows are ordered by generation with attempt counts from the
    execution ledger. Payloads, headers and capabilities are omitted. Next_generation is the after
    watermark for the next page.

    Args:
        slug (str):
        id (UUID):
        after (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationExecutionsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            after=after,
            limit=limit,
        )
    ).parsed
