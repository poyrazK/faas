from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.create_data_pipeline_request import CreateDataPipelineRequest
from ...models.data_pipeline import DataPipeline
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateDataPipelineRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/data/pipelines",
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
    *,
    client: AuthenticatedClient | Client,
    body: CreateDataPipelineRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DataPipeline | Problem]:
    """Start a managed PostgreSQL table mirror

    Args:
        idempotency_key (str | Unset):
        body (CreateDataPipelineRequest): Selected public tables, dedicated destination and
            explicitly authorized JWT readers. A fresh passing connection check for the current source
            credential and all selected tables is required; otherwise returns
            data_connection_check_required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataPipeline | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateDataPipelineRequest,
    idempotency_key: str | Unset = UNSET,
) -> DataPipeline | Problem | None:
    """Start a managed PostgreSQL table mirror

    Args:
        idempotency_key (str | Unset):
        body (CreateDataPipelineRequest): Selected public tables, dedicated destination and
            explicitly authorized JWT readers. A fresh passing connection check for the current source
            credential and all selected tables is required; otherwise returns
            data_connection_check_required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataPipeline | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateDataPipelineRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[DataPipeline | Problem]:
    """Start a managed PostgreSQL table mirror

    Args:
        idempotency_key (str | Unset):
        body (CreateDataPipelineRequest): Selected public tables, dedicated destination and
            explicitly authorized JWT readers. A fresh passing connection check for the current source
            credential and all selected tables is required; otherwise returns
            data_connection_check_required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataPipeline | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateDataPipelineRequest,
    idempotency_key: str | Unset = UNSET,
) -> DataPipeline | Problem | None:
    """Start a managed PostgreSQL table mirror

    Args:
        idempotency_key (str | Unset):
        body (CreateDataPipelineRequest): Selected public tables, dedicated destination and
            explicitly authorized JWT readers. A fresh passing connection check for the current source
            credential and all selected tables is required; otherwise returns
            data_connection_check_required.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataPipeline | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
