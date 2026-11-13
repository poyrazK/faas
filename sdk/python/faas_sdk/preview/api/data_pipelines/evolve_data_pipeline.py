from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.data_pipeline import DataPipeline
from ...models.data_pipeline_evolution_request import DataPipelineEvolutionRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: DataPipelineEvolutionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/data/pipelines/{id}/evolution".format(
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
    body: DataPipelineEvolutionRequest,
) -> Response[DataPipeline | Problem]:
    """Preview or approve pipeline serving evolution

     Save an additive projection, Auth organization mapping, or declared relationship preview. Approval
    supplies only its saved ID and revision. Projection changes subsequently require the exact source
    schema or table approval before copying and exposure.

    Args:
        id (UUID):
        body (DataPipelineEvolutionRequest): Revision-fenced owner evolution action. Preview
            supplies one kind and its complete selection or mapping. Approve and discard supply only a
            saved change_id; approval never accepts replacement intent.

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
    body: DataPipelineEvolutionRequest,
) -> DataPipeline | Problem | None:
    """Preview or approve pipeline serving evolution

     Save an additive projection, Auth organization mapping, or declared relationship preview. Approval
    supplies only its saved ID and revision. Projection changes subsequently require the exact source
    schema or table approval before copying and exposure.

    Args:
        id (UUID):
        body (DataPipelineEvolutionRequest): Revision-fenced owner evolution action. Preview
            supplies one kind and its complete selection or mapping. Approve and discard supply only a
            saved change_id; approval never accepts replacement intent.

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
    body: DataPipelineEvolutionRequest,
) -> Response[DataPipeline | Problem]:
    """Preview or approve pipeline serving evolution

     Save an additive projection, Auth organization mapping, or declared relationship preview. Approval
    supplies only its saved ID and revision. Projection changes subsequently require the exact source
    schema or table approval before copying and exposure.

    Args:
        id (UUID):
        body (DataPipelineEvolutionRequest): Revision-fenced owner evolution action. Preview
            supplies one kind and its complete selection or mapping. Approve and discard supply only a
            saved change_id; approval never accepts replacement intent.

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
    body: DataPipelineEvolutionRequest,
) -> DataPipeline | Problem | None:
    """Preview or approve pipeline serving evolution

     Save an additive projection, Auth organization mapping, or declared relationship preview. Approval
    supplies only its saved ID and revision. Projection changes subsequently require the exact source
    schema or table approval before copying and exposure.

    Args:
        id (UUID):
        body (DataPipelineEvolutionRequest): Revision-fenced owner evolution action. Preview
            supplies one kind and its complete selection or mapping. Approve and discard supply only a
            saved change_id; approval never accepts replacement intent.

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
