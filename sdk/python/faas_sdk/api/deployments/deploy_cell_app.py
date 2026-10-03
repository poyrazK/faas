from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_deployment_request import CreateDeploymentRequest
from ...models.deploy_cell_app_files_body import DeployCellAppFilesBody
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    cell_id: UUID,
    app_id: UUID,
    *,
    body: CreateDeploymentRequest | DeployCellAppFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/cells/{cell_id}/apps/{app_id}/deployments".format(
            cell_id=quote(str(cell_id), safe=""),
            app_id=quote(str(app_id), safe=""),
        ),
    }

    if isinstance(body, CreateDeploymentRequest):
        _kwargs["json"] = body.to_dict()

        headers["Content-Type"] = "application/json"
    if isinstance(body, DeployCellAppFilesBody):
        _kwargs["files"] = body.to_multipart()

        # httpx supplies the boundary for the encoded files.

    _kwargs["headers"] = headers
    _kwargs["follow_redirects"] = False
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentResponse | Problem | None:
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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[DeploymentResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    cell_id: UUID,
    app_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest | DeployCellAppFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Create deployment intent for an immutable regional app.

     Requires a personal-account regional API key with admin or deploy-write
    scope, MFA when required, and a verified email. Both canonical nonzero
    UUIDs must identify this configured cell and a personally owned live app.
    Identity and authorization are checked before idempotent replay. Slug
    reuse cannot redirect this mutation. Redirects must not be followed.
    Acceptance creates regional intent and does not report global readiness.
    Two content-types are accepted:
    - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
    - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
    Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
    The optional `workflows` array is plan-gated and schema-validated;
    accepted definitions are persisted with the deployment and snapshotted
    when a workflow run starts.

    Args:
        cell_id (UUID):
        app_id (UUID):
        idempotency_key (str | Unset):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / startup healthcheck /
            readiness_probe / liveness_probe without rebuilding the image. The optional `companions`
            array attaches bounded helper workloads such as an OpenTelemetry collector, database
            proxy, or reverse proxy. The deprecated `sidecars` spelling remains accepted for existing
            clients.
        body (DeployCellAppFilesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        cell_id=cell_id,
        app_id=app_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    cell_id: UUID,
    app_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest | DeployCellAppFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Create deployment intent for an immutable regional app.

     Requires a personal-account regional API key with admin or deploy-write
    scope, MFA when required, and a verified email. Both canonical nonzero
    UUIDs must identify this configured cell and a personally owned live app.
    Identity and authorization are checked before idempotent replay. Slug
    reuse cannot redirect this mutation. Redirects must not be followed.
    Acceptance creates regional intent and does not report global readiness.
    Two content-types are accepted:
    - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
    - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
    Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
    The optional `workflows` array is plan-gated and schema-validated;
    accepted definitions are persisted with the deployment and snapshotted
    when a workflow run starts.

    Args:
        cell_id (UUID):
        app_id (UUID):
        idempotency_key (str | Unset):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / startup healthcheck /
            readiness_probe / liveness_probe without rebuilding the image. The optional `companions`
            array attaches bounded helper workloads such as an OpenTelemetry collector, database
            proxy, or reverse proxy. The deprecated `sidecars` spelling remains accepted for existing
            clients.
        body (DeployCellAppFilesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return sync_detailed(
        cell_id=cell_id,
        app_id=app_id,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    cell_id: UUID,
    app_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest | DeployCellAppFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Create deployment intent for an immutable regional app.

     Requires a personal-account regional API key with admin or deploy-write
    scope, MFA when required, and a verified email. Both canonical nonzero
    UUIDs must identify this configured cell and a personally owned live app.
    Identity and authorization are checked before idempotent replay. Slug
    reuse cannot redirect this mutation. Redirects must not be followed.
    Acceptance creates regional intent and does not report global readiness.
    Two content-types are accepted:
    - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
    - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
    Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
    The optional `workflows` array is plan-gated and schema-validated;
    accepted definitions are persisted with the deployment and snapshotted
    when a workflow run starts.

    Args:
        cell_id (UUID):
        app_id (UUID):
        idempotency_key (str | Unset):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / startup healthcheck /
            readiness_probe / liveness_probe without rebuilding the image. The optional `companions`
            array attaches bounded helper workloads such as an OpenTelemetry collector, database
            proxy, or reverse proxy. The deprecated `sidecars` spelling remains accepted for existing
            clients.
        body (DeployCellAppFilesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        cell_id=cell_id,
        app_id=app_id,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    cell_id: UUID,
    app_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest | DeployCellAppFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Create deployment intent for an immutable regional app.

     Requires a personal-account regional API key with admin or deploy-write
    scope, MFA when required, and a verified email. Both canonical nonzero
    UUIDs must identify this configured cell and a personally owned live app.
    Identity and authorization are checked before idempotent replay. Slug
    reuse cannot redirect this mutation. Redirects must not be followed.
    Acceptance creates regional intent and does not report global readiness.
    Two content-types are accepted:
    - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
    - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
    Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
    The optional `workflows` array is plan-gated and schema-validated;
    accepted definitions are persisted with the deployment and snapshotted
    when a workflow run starts.

    Args:
        cell_id (UUID):
        app_id (UUID):
        idempotency_key (str | Unset):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / startup healthcheck /
            readiness_probe / liveness_probe without rebuilding the image. The optional `companions`
            array attaches bounded helper workloads such as an OpenTelemetry collector, database
            proxy, or reverse proxy. The deprecated `sidecars` spelling remains accepted for existing
            clients.
        body (DeployCellAppFilesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return (
        await asyncio_detailed(
            cell_id=cell_id,
            app_id=app_id,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
