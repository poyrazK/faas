from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...models.update_deployment_min_instances_body import UpdateDeploymentMinInstancesBody
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: UpdateDeploymentMinInstancesBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/deployments/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DeploymentResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateDeploymentMinInstancesBody,
) -> Response[DeploymentResponse | Problem]:
    """Set the per-deployment cold-wake floor.

     Update the deployment's min_instances (issue #557 closure /
    ADR-072). The only mutable field on a deployment post-create;
    image / digest / overrides / sidecars stay immutable (a new
    deployment is the canonical way to change them). Pass
    min_instances=0 to inherit from the parent app's floor.
    Validated against the parent app's plan MaxMinInstances cap.

    Args:
        id (str):
        body (UpdateDeploymentMinInstancesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateDeploymentMinInstancesBody,
) -> DeploymentResponse | Problem | None:
    """Set the per-deployment cold-wake floor.

     Update the deployment's min_instances (issue #557 closure /
    ADR-072). The only mutable field on a deployment post-create;
    image / digest / overrides / sidecars stay immutable (a new
    deployment is the canonical way to change them). Pass
    min_instances=0 to inherit from the parent app's floor.
    Validated against the parent app's plan MaxMinInstances cap.

    Args:
        id (str):
        body (UpdateDeploymentMinInstancesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateDeploymentMinInstancesBody,
) -> Response[DeploymentResponse | Problem]:
    """Set the per-deployment cold-wake floor.

     Update the deployment's min_instances (issue #557 closure /
    ADR-072). The only mutable field on a deployment post-create;
    image / digest / overrides / sidecars stay immutable (a new
    deployment is the canonical way to change them). Pass
    min_instances=0 to inherit from the parent app's floor.
    Validated against the parent app's plan MaxMinInstances cap.

    Args:
        id (str):
        body (UpdateDeploymentMinInstancesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateDeploymentMinInstancesBody,
) -> DeploymentResponse | Problem | None:
    """Set the per-deployment cold-wake floor.

     Update the deployment's min_instances (issue #557 closure /
    ADR-072). The only mutable field on a deployment post-create;
    image / digest / overrides / sidecars stay immutable (a new
    deployment is the canonical way to change them). Pass
    min_instances=0 to inherit from the parent app's floor.
    Validated against the parent app's plan MaxMinInstances cap.

    Args:
        id (str):
        body (UpdateDeploymentMinInstancesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
