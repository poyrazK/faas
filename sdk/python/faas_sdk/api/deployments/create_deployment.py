from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_deployment_files_body import CreateDeploymentFilesBody
from ...models.create_deployment_request import CreateDeploymentRequest
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateDeploymentRequest | CreateDeploymentFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    if isinstance(body, CreateDeploymentRequest):
        _kwargs["json"] = body.to_dict()

        headers["Content-Type"] = "application/json"
    if isinstance(body, CreateDeploymentFilesBody):
        _kwargs["files"] = body.to_multipart()

        headers["Content-Type"] = "multipart/form-data; boundary=+++"

    _kwargs["headers"] = headers
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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

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
    body: CreateDeploymentRequest | CreateDeploymentFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Create a deployment.

     Two content-types are accepted:
    - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
    - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
    Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
    The optional `workflows` array is plan-gated and schema-validated;
    until workflow runtime persistence is enabled, a request containing
    workflow definitions returns `501 workflow_deployment_unavailable`
    rather than accepting and dropping them.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / healthcheck without rebuilding the
            image. The override field list is FROZEN — six fields, no more — and any extra field on
            the override object 400s the request (the handler's decoder rejects unknown keys; see
            ADR-053 §Decision 1). The optional `sidecars` array (issue #463 / ADR-068) attaches up to
            2 stateless sidecars (1 init + 1 sidecar) per app — a one-shot DB migrator as `init`, a
            metrics scraper as `sidecar`. nil/omitted = no sidecars.
        body (CreateDeploymentFilesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest | CreateDeploymentFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Create a deployment.

     Two content-types are accepted:
    - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
    - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
    Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
    The optional `workflows` array is plan-gated and schema-validated;
    until workflow runtime persistence is enabled, a request containing
    workflow definitions returns `501 workflow_deployment_unavailable`
    rather than accepting and dropping them.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / healthcheck without rebuilding the
            image. The override field list is FROZEN — six fields, no more — and any extra field on
            the override object 400s the request (the handler's decoder rejects unknown keys; see
            ADR-053 §Decision 1). The optional `sidecars` array (issue #463 / ADR-068) attaches up to
            2 stateless sidecars (1 init + 1 sidecar) per app — a one-shot DB migrator as `init`, a
            metrics scraper as `sidecar`. nil/omitted = no sidecars.
        body (CreateDeploymentFilesBody):

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
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest | CreateDeploymentFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Create a deployment.

     Two content-types are accepted:
    - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
    - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
    Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
    The optional `workflows` array is plan-gated and schema-validated;
    until workflow runtime persistence is enabled, a request containing
    workflow definitions returns `501 workflow_deployment_unavailable`
    rather than accepting and dropping them.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / healthcheck without rebuilding the
            image. The override field list is FROZEN — six fields, no more — and any extra field on
            the override object 400s the request (the handler's decoder rejects unknown keys; see
            ADR-053 §Decision 1). The optional `sidecars` array (issue #463 / ADR-068) attaches up to
            2 stateless sidecars (1 init + 1 sidecar) per app — a one-shot DB migrator as `init`, a
            metrics scraper as `sidecar`. nil/omitted = no sidecars.
        body (CreateDeploymentFilesBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateDeploymentRequest | CreateDeploymentFilesBody | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Create a deployment.

     Two content-types are accepted:
    - `application/json` (`CreateDeploymentRequest` with an `image` field): prebuilt OCI reference.
    - `multipart/form-data`: source tarball upload (or Dockerfile escape hatch).
    Source size is plan-capped (Free/Hobby 100 MB, Pro/Scale 250 MB).
    The optional `workflows` array is plan-gated and schema-validated;
    until workflow runtime persistence is enabled, a request containing
    workflow definitions returns `501 workflow_deployment_unavailable`
    rather than accepting and dropping them.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDeploymentRequest): Two content-types accepted (see operation description):
            prebuilt OCI image reference, or multipart source upload. The optional `overrides` object
            (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a
            different entrypoint / cmd / env / env_secrets / port / healthcheck without rebuilding the
            image. The override field list is FROZEN — six fields, no more — and any extra field on
            the override object 400s the request (the handler's decoder rejects unknown keys; see
            ADR-053 §Decision 1). The optional `sidecars` array (issue #463 / ADR-068) attaches up to
            2 stateless sidecars (1 init + 1 sidecar) per app — a one-shot DB migrator as `init`, a
            metrics scraper as `sidecar`. nil/omitted = no sidecars.
        body (CreateDeploymentFilesBody):

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
            idempotency_key=idempotency_key,
        )
    ).parsed
