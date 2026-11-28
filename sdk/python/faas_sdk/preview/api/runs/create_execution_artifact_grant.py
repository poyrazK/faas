from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_execution_artifact_grant_request import CreateExecutionArtifactGrantRequest
from ...models.execution_artifact_grant_response import ExecutionArtifactGrantResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: CreateExecutionArtifactGrantRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/executions/{id}/artifact-grants".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ExecutionArtifactGrantResponse | Problem | None:
    if response.status_code == 201:
        response_201 = ExecutionArtifactGrantResponse.from_dict(response.json())

        return response_201

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ExecutionArtifactGrantResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateExecutionArtifactGrantRequest,
) -> Response[ExecutionArtifactGrantResponse | Problem]:
    """Share one output artifact with another agent.

     Creates a single-use, short-lived capability for one named artifact on
    a successful execution. The creating Runs key must own the source run;
    account-wide principals may grant any account run. The bearer token is
    returned once and only its hash is stored. The recipient can stage only
    these bytes using `artifact_inputs.grant_token`; this does not grant
    access to the source receipt, events, cancellation, or other artifacts.
    Requires `runs:write`, `deploy:write`, or `admin`.

    Args:
        id (UUID):
        body (CreateExecutionArtifactGrantRequest): Request to create a short-lived, one-time
            capability for one output artifact.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionArtifactGrantResponse | Problem]
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
    client: AuthenticatedClient,
    body: CreateExecutionArtifactGrantRequest,
) -> ExecutionArtifactGrantResponse | Problem | None:
    """Share one output artifact with another agent.

     Creates a single-use, short-lived capability for one named artifact on
    a successful execution. The creating Runs key must own the source run;
    account-wide principals may grant any account run. The bearer token is
    returned once and only its hash is stored. The recipient can stage only
    these bytes using `artifact_inputs.grant_token`; this does not grant
    access to the source receipt, events, cancellation, or other artifacts.
    Requires `runs:write`, `deploy:write`, or `admin`.

    Args:
        id (UUID):
        body (CreateExecutionArtifactGrantRequest): Request to create a short-lived, one-time
            capability for one output artifact.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionArtifactGrantResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient,
    body: CreateExecutionArtifactGrantRequest,
) -> Response[ExecutionArtifactGrantResponse | Problem]:
    """Share one output artifact with another agent.

     Creates a single-use, short-lived capability for one named artifact on
    a successful execution. The creating Runs key must own the source run;
    account-wide principals may grant any account run. The bearer token is
    returned once and only its hash is stored. The recipient can stage only
    these bytes using `artifact_inputs.grant_token`; this does not grant
    access to the source receipt, events, cancellation, or other artifacts.
    Requires `runs:write`, `deploy:write`, or `admin`.

    Args:
        id (UUID):
        body (CreateExecutionArtifactGrantRequest): Request to create a short-lived, one-time
            capability for one output artifact.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ExecutionArtifactGrantResponse | Problem]
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
    client: AuthenticatedClient,
    body: CreateExecutionArtifactGrantRequest,
) -> ExecutionArtifactGrantResponse | Problem | None:
    """Share one output artifact with another agent.

     Creates a single-use, short-lived capability for one named artifact on
    a successful execution. The creating Runs key must own the source run;
    account-wide principals may grant any account run. The bearer token is
    returned once and only its hash is stored. The recipient can stage only
    these bytes using `artifact_inputs.grant_token`; this does not grant
    access to the source receipt, events, cancellation, or other artifacts.
    Requires `runs:write`, `deploy:write`, or `admin`.

    Args:
        id (UUID):
        body (CreateExecutionArtifactGrantRequest): Request to create a short-lived, one-time
            capability for one output artifact.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ExecutionArtifactGrantResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
