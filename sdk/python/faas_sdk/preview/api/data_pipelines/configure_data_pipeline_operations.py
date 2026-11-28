from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.data_pipeline import DataPipeline
from ...models.data_pipeline_operations_request import DataPipelineOperationsRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: DataPipelineOperationsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/data/pipelines/{id}/operations".format(
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
    body: DataPipelineOperationsRequest,
) -> Response[DataPipeline | Problem]:
    """Configure scheduled pipeline work

     Set bounded snapshot schedules, curated quality rules and UTC work budgets, or request the next
    snapshot or quality run. Import mode and serving selection remain immutable. Manual requests respect
    work windows.

    Args:
        id (UUID):
        body (DataPipelineOperationsRequest): Revision-checked replacement maintenance
            configuration or an explicit snapshot/quality run request. Configure supplies config; run
            actions omit it and still respect the saved UTC window.

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
    body: DataPipelineOperationsRequest,
) -> DataPipeline | Problem | None:
    """Configure scheduled pipeline work

     Set bounded snapshot schedules, curated quality rules and UTC work budgets, or request the next
    snapshot or quality run. Import mode and serving selection remain immutable. Manual requests respect
    work windows.

    Args:
        id (UUID):
        body (DataPipelineOperationsRequest): Revision-checked replacement maintenance
            configuration or an explicit snapshot/quality run request. Configure supplies config; run
            actions omit it and still respect the saved UTC window.

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
    body: DataPipelineOperationsRequest,
) -> Response[DataPipeline | Problem]:
    """Configure scheduled pipeline work

     Set bounded snapshot schedules, curated quality rules and UTC work budgets, or request the next
    snapshot or quality run. Import mode and serving selection remain immutable. Manual requests respect
    work windows.

    Args:
        id (UUID):
        body (DataPipelineOperationsRequest): Revision-checked replacement maintenance
            configuration or an explicit snapshot/quality run request. Configure supplies config; run
            actions omit it and still respect the saved UTC window.

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
    body: DataPipelineOperationsRequest,
) -> DataPipeline | Problem | None:
    """Configure scheduled pipeline work

     Set bounded snapshot schedules, curated quality rules and UTC work budgets, or request the next
    snapshot or quality run. Import mode and serving selection remain immutable. Manual requests respect
    work windows.

    Args:
        id (UUID):
        body (DataPipelineOperationsRequest): Revision-checked replacement maintenance
            configuration or an explicit snapshot/quality run request. Configure supplies config; run
            actions omit it and still respect the saved UTC window.

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
