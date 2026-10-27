from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_deployment_request import CreateDeploymentRequest
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateDeploymentRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/image-published".format(
            slug=quote(str(slug), safe=""),
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

    if response.status_code == 202:
        response_202 = DeploymentResponse.from_dict(response.json())

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
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest,
) -> Response[DeploymentResponse | Problem]:
    """Deploy an immutable image after CI publishes it.

     Requires deploy:write or admin and the normal deployment authentication
    checks. App-bound deploy tokens and CI bearer tokens are accepted.
    The app must be a project workload configured with image:. The required
    image is a full registry/repository@sha256:digest reference matching that
    workload's declared registry and repository. A digest-pinned declaration
    accepts only its declared digest. Tags are rejected; send the digest
    returned by the build/push step after publication completes.

    App, normalized deployment scope, and image reference form a durable
    delivery identity. First delivery creates one pending OCI deployment
    without a source build. Repeated deliveries return the original row and
    its current status, including terminal states, without creating a new
    deployment. The first accepted configuration wins for this identity;
    use ordinary deployment or retry endpoints for intentional redeploys.
    Changing scope creates an independent delivery.

    Normal signature, security, plan, traffic, and account deploy-rate
    admission apply. The Compose main container port defaults the port
    override. Omitted workflows and the source release command inherit from
    the latest image deployment in the same scope. Image commands, service
    bindings, and scoped environment settings use the existing app contract.
    Optional overrides and rollout policies follow CreateDeploymentRequest.
    The project image declaration is retained. This is a CI handoff endpoint;
    native registry webhook payloads and registry polling are not supported.

    Args:
        slug (str):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / startup healthcheck /
            readiness_probe / liveness_probe without rebuilding the image. The optional `companions`
            array attaches bounded helper workloads such as an OpenTelemetry collector, database
            proxy, or reverse proxy. The deprecated `sidecars` spelling remains accepted for existing
            clients.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest,
) -> DeploymentResponse | Problem | None:
    """Deploy an immutable image after CI publishes it.

     Requires deploy:write or admin and the normal deployment authentication
    checks. App-bound deploy tokens and CI bearer tokens are accepted.
    The app must be a project workload configured with image:. The required
    image is a full registry/repository@sha256:digest reference matching that
    workload's declared registry and repository. A digest-pinned declaration
    accepts only its declared digest. Tags are rejected; send the digest
    returned by the build/push step after publication completes.

    App, normalized deployment scope, and image reference form a durable
    delivery identity. First delivery creates one pending OCI deployment
    without a source build. Repeated deliveries return the original row and
    its current status, including terminal states, without creating a new
    deployment. The first accepted configuration wins for this identity;
    use ordinary deployment or retry endpoints for intentional redeploys.
    Changing scope creates an independent delivery.

    Normal signature, security, plan, traffic, and account deploy-rate
    admission apply. The Compose main container port defaults the port
    override. Omitted workflows and the source release command inherit from
    the latest image deployment in the same scope. Image commands, service
    bindings, and scoped environment settings use the existing app contract.
    Optional overrides and rollout policies follow CreateDeploymentRequest.
    The project image declaration is retained. This is a CI handoff endpoint;
    native registry webhook payloads and registry polling are not supported.

    Args:
        slug (str):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / startup healthcheck /
            readiness_probe / liveness_probe without rebuilding the image. The optional `companions`
            array attaches bounded helper workloads such as an OpenTelemetry collector, database
            proxy, or reverse proxy. The deprecated `sidecars` spelling remains accepted for existing
            clients.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest,
) -> Response[DeploymentResponse | Problem]:
    """Deploy an immutable image after CI publishes it.

     Requires deploy:write or admin and the normal deployment authentication
    checks. App-bound deploy tokens and CI bearer tokens are accepted.
    The app must be a project workload configured with image:. The required
    image is a full registry/repository@sha256:digest reference matching that
    workload's declared registry and repository. A digest-pinned declaration
    accepts only its declared digest. Tags are rejected; send the digest
    returned by the build/push step after publication completes.

    App, normalized deployment scope, and image reference form a durable
    delivery identity. First delivery creates one pending OCI deployment
    without a source build. Repeated deliveries return the original row and
    its current status, including terminal states, without creating a new
    deployment. The first accepted configuration wins for this identity;
    use ordinary deployment or retry endpoints for intentional redeploys.
    Changing scope creates an independent delivery.

    Normal signature, security, plan, traffic, and account deploy-rate
    admission apply. The Compose main container port defaults the port
    override. Omitted workflows and the source release command inherit from
    the latest image deployment in the same scope. Image commands, service
    bindings, and scoped environment settings use the existing app contract.
    Optional overrides and rollout policies follow CreateDeploymentRequest.
    The project image declaration is retained. This is a CI handoff endpoint;
    native registry webhook payloads and registry polling are not supported.

    Args:
        slug (str):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / startup healthcheck /
            readiness_probe / liveness_probe without rebuilding the image. The optional `companions`
            array attaches bounded helper workloads such as an OpenTelemetry collector, database
            proxy, or reverse proxy. The deprecated `sidecars` spelling remains accepted for existing
            clients.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest,
) -> DeploymentResponse | Problem | None:
    """Deploy an immutable image after CI publishes it.

     Requires deploy:write or admin and the normal deployment authentication
    checks. App-bound deploy tokens and CI bearer tokens are accepted.
    The app must be a project workload configured with image:. The required
    image is a full registry/repository@sha256:digest reference matching that
    workload's declared registry and repository. A digest-pinned declaration
    accepts only its declared digest. Tags are rejected; send the digest
    returned by the build/push step after publication completes.

    App, normalized deployment scope, and image reference form a durable
    delivery identity. First delivery creates one pending OCI deployment
    without a source build. Repeated deliveries return the original row and
    its current status, including terminal states, without creating a new
    deployment. The first accepted configuration wins for this identity;
    use ordinary deployment or retry endpoints for intentional redeploys.
    Changing scope creates an independent delivery.

    Normal signature, security, plan, traffic, and account deploy-rate
    admission apply. The Compose main container port defaults the port
    override. Omitted workflows and the source release command inherit from
    the latest image deployment in the same scope. Image commands, service
    bindings, and scoped environment settings use the existing app contract.
    Optional overrides and rollout policies follow CreateDeploymentRequest.
    The project image declaration is retained. This is a CI handoff endpoint;
    native registry webhook payloads and registry polling are not supported.

    Args:
        slug (str):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / startup healthcheck /
            readiness_probe / liveness_probe without rebuilding the image. The optional `companions`
            array attaches bounded helper workloads such as an OpenTelemetry collector, database
            proxy, or reverse proxy. The deprecated `sidecars` spelling remains accepted for existing
            clients.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
