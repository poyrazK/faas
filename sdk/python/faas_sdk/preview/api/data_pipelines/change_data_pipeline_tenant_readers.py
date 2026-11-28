from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.data_pipeline import DataPipeline
from ...models.data_pipeline_tenant_readers_request import DataPipelineTenantReadersRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: DataPipelineTenantReadersRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/data/pipelines/{id}/tenant-readers".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> DataPipeline | Problem:
    if response.status_code == 202:
        response_202 = DataPipeline.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataPipeline | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DataPipelineTenantReadersRequest,
) -> Response[DataPipeline | Problem]:
    """Preview or approve an exact tenant membership change

    Args:
        id (UUID):
        body (DataPipelineTenantReadersRequest): Account-owner-only, revision-fenced membership
            preview, approval or discard. Preview supplies the complete replacement membership array.
            Approve supplies only the saved change_id; grace permits strict membership reductions
            only.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataPipeline | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DataPipelineTenantReadersRequest,
) -> DataPipeline | Problem | None:
    """Preview or approve an exact tenant membership change

    Args:
        id (UUID):
        body (DataPipelineTenantReadersRequest): Account-owner-only, revision-fenced membership
            preview, approval or discard. Preview supplies the complete replacement membership array.
            Approve supplies only the saved change_id; grace permits strict membership reductions
            only.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataPipeline | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DataPipelineTenantReadersRequest,
) -> Response[DataPipeline | Problem]:
    """Preview or approve an exact tenant membership change

    Args:
        id (UUID):
        body (DataPipelineTenantReadersRequest): Account-owner-only, revision-fenced membership
            preview, approval or discard. Preview supplies the complete replacement membership array.
            Approve supplies only the saved change_id; grace permits strict membership reductions
            only.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataPipeline | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: DataPipelineTenantReadersRequest,
) -> DataPipeline | Problem | None:
    """Preview or approve an exact tenant membership change

    Args:
        id (UUID):
        body (DataPipelineTenantReadersRequest): Account-owner-only, revision-fenced membership
            preview, approval or discard. Preview supplies the complete replacement membership array.
            Approve supplies only the saved change_id; grace permits strict membership reductions
            only.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataPipeline | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
