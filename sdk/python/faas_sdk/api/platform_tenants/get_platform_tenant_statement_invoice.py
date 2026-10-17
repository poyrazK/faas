from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.statement_invoice_export_response import StatementInvoiceExportResponse
from ...types import Response


def _get_kwargs(
    id: UUID,
    statement_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/platform-tenants/{id}/usage-statements/{statement_id}/invoice".format(
            id=quote(str(id), safe=""),
            statement_id=quote(str(statement_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | StatementInvoiceExportResponse | None:
    if response.status_code == 200:
        response_200 = StatementInvoiceExportResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | StatementInvoiceExportResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Read the platform tenant statement's invoice export.

    Args:
        id (UUID):
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        statement_id=statement_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | StatementInvoiceExportResponse | None:
    """Read the platform tenant statement's invoice export.

    Args:
        id (UUID):
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
    """

    return sync_detailed(
        id=id,
        statement_id=statement_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | StatementInvoiceExportResponse]:
    """Read the platform tenant statement's invoice export.

    Args:
        id (UUID):
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StatementInvoiceExportResponse]
    """

    kwargs = _get_kwargs(
        id=id,
        statement_id=statement_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    statement_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | StatementInvoiceExportResponse | None:
    """Read the platform tenant statement's invoice export.

    Args:
        id (UUID):
        statement_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StatementInvoiceExportResponse
    """

    return (
        await asyncio_detailed(
            id=id,
            statement_id=statement_id,
            client=client,
        )
    ).parsed
