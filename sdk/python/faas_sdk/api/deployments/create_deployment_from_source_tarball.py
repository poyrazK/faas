from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_deployment_from_source_tarball_body import CreateDeploymentFromSourceTarballBody
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateDeploymentFromSourceTarballBody,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments/source-tarball".format(
            slug=quote(str(slug), safe=""),
        ),
    }

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
    body: CreateDeploymentFromSourceTarballBody,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    r"""Create a deployment from a CLI-uploaded local tarball (zero-config).

     Zero-config deploy path (issue #961 / Mega-A PR-1, ADR-115).
    The CLI uploads a gzipped tar via the `tarball` form field and
    an optional informational `{repo, ref}` JSON sidecar. The CLI
    binary is the trust root: apid does NOT consult
    `github_installations` and does NOT attempt a server-side git
    fetch.

    Distinct from the source-ref path
    (`POST /v1/apps/{slug}/deployments/source-ref`, ADR-092) which
    resolves the GitHub install and pins the tarball to a 40-char
    SHA. The source-ref handler is unchanged; this is a parallel
    trust path for first-deploy customers without the GitHub App
    installed.

    Wire shape:
      multipart/form-data with two fields:
        - `tarball` (required): the gzipped tar, capped at the
          per-plan `SourceTarballMaxMB`.
        - `sidecar` (optional): JSON `{repo, ref}` recorded on
          the build row for provenance only. The build pipeline
          does NOT use the sidecar to fetch upstream — the tarball
          bytes are the build source, and the sidecar is purely
          informational. Operators relying on source-pinning MUST
          use the source-ref path instead.

    Lifecycle (issue #1182 fix): the refactored zero-config
    CLI path runs `POST /v1/apps` (CreateApp) BEFORE this endpoint,
    so a brand-new slug gets a 201 from CreateApp and a 202 from
    this endpoint. A direct hit on this endpoint with a slug that
    has never been created returns 404 — pre-#1182 zero-config
    customers hit this with \"no such app\"; the fix folds the
    path through CreateApp so the slug always exists by the time
    this endpoint is reached.

    Audit kind: `deploy.local_tarball` (distinct from
    `deploy.source_ref`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDeploymentFromSourceTarballBody):

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
    body: CreateDeploymentFromSourceTarballBody,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    r"""Create a deployment from a CLI-uploaded local tarball (zero-config).

     Zero-config deploy path (issue #961 / Mega-A PR-1, ADR-115).
    The CLI uploads a gzipped tar via the `tarball` form field and
    an optional informational `{repo, ref}` JSON sidecar. The CLI
    binary is the trust root: apid does NOT consult
    `github_installations` and does NOT attempt a server-side git
    fetch.

    Distinct from the source-ref path
    (`POST /v1/apps/{slug}/deployments/source-ref`, ADR-092) which
    resolves the GitHub install and pins the tarball to a 40-char
    SHA. The source-ref handler is unchanged; this is a parallel
    trust path for first-deploy customers without the GitHub App
    installed.

    Wire shape:
      multipart/form-data with two fields:
        - `tarball` (required): the gzipped tar, capped at the
          per-plan `SourceTarballMaxMB`.
        - `sidecar` (optional): JSON `{repo, ref}` recorded on
          the build row for provenance only. The build pipeline
          does NOT use the sidecar to fetch upstream — the tarball
          bytes are the build source, and the sidecar is purely
          informational. Operators relying on source-pinning MUST
          use the source-ref path instead.

    Lifecycle (issue #1182 fix): the refactored zero-config
    CLI path runs `POST /v1/apps` (CreateApp) BEFORE this endpoint,
    so a brand-new slug gets a 201 from CreateApp and a 202 from
    this endpoint. A direct hit on this endpoint with a slug that
    has never been created returns 404 — pre-#1182 zero-config
    customers hit this with \"no such app\"; the fix folds the
    path through CreateApp so the slug always exists by the time
    this endpoint is reached.

    Audit kind: `deploy.local_tarball` (distinct from
    `deploy.source_ref`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDeploymentFromSourceTarballBody):

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
    body: CreateDeploymentFromSourceTarballBody,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    r"""Create a deployment from a CLI-uploaded local tarball (zero-config).

     Zero-config deploy path (issue #961 / Mega-A PR-1, ADR-115).
    The CLI uploads a gzipped tar via the `tarball` form field and
    an optional informational `{repo, ref}` JSON sidecar. The CLI
    binary is the trust root: apid does NOT consult
    `github_installations` and does NOT attempt a server-side git
    fetch.

    Distinct from the source-ref path
    (`POST /v1/apps/{slug}/deployments/source-ref`, ADR-092) which
    resolves the GitHub install and pins the tarball to a 40-char
    SHA. The source-ref handler is unchanged; this is a parallel
    trust path for first-deploy customers without the GitHub App
    installed.

    Wire shape:
      multipart/form-data with two fields:
        - `tarball` (required): the gzipped tar, capped at the
          per-plan `SourceTarballMaxMB`.
        - `sidecar` (optional): JSON `{repo, ref}` recorded on
          the build row for provenance only. The build pipeline
          does NOT use the sidecar to fetch upstream — the tarball
          bytes are the build source, and the sidecar is purely
          informational. Operators relying on source-pinning MUST
          use the source-ref path instead.

    Lifecycle (issue #1182 fix): the refactored zero-config
    CLI path runs `POST /v1/apps` (CreateApp) BEFORE this endpoint,
    so a brand-new slug gets a 201 from CreateApp and a 202 from
    this endpoint. A direct hit on this endpoint with a slug that
    has never been created returns 404 — pre-#1182 zero-config
    customers hit this with \"no such app\"; the fix folds the
    path through CreateApp so the slug always exists by the time
    this endpoint is reached.

    Audit kind: `deploy.local_tarball` (distinct from
    `deploy.source_ref`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDeploymentFromSourceTarballBody):

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
    body: CreateDeploymentFromSourceTarballBody,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    r"""Create a deployment from a CLI-uploaded local tarball (zero-config).

     Zero-config deploy path (issue #961 / Mega-A PR-1, ADR-115).
    The CLI uploads a gzipped tar via the `tarball` form field and
    an optional informational `{repo, ref}` JSON sidecar. The CLI
    binary is the trust root: apid does NOT consult
    `github_installations` and does NOT attempt a server-side git
    fetch.

    Distinct from the source-ref path
    (`POST /v1/apps/{slug}/deployments/source-ref`, ADR-092) which
    resolves the GitHub install and pins the tarball to a 40-char
    SHA. The source-ref handler is unchanged; this is a parallel
    trust path for first-deploy customers without the GitHub App
    installed.

    Wire shape:
      multipart/form-data with two fields:
        - `tarball` (required): the gzipped tar, capped at the
          per-plan `SourceTarballMaxMB`.
        - `sidecar` (optional): JSON `{repo, ref}` recorded on
          the build row for provenance only. The build pipeline
          does NOT use the sidecar to fetch upstream — the tarball
          bytes are the build source, and the sidecar is purely
          informational. Operators relying on source-pinning MUST
          use the source-ref path instead.

    Lifecycle (issue #1182 fix): the refactored zero-config
    CLI path runs `POST /v1/apps` (CreateApp) BEFORE this endpoint,
    so a brand-new slug gets a 201 from CreateApp and a 202 from
    this endpoint. A direct hit on this endpoint with a slug that
    has never been created returns 404 — pre-#1182 zero-config
    customers hit this with \"no such app\"; the fix folds the
    path through CreateApp so the slug always exists by the time
    this endpoint is reached.

    Audit kind: `deploy.local_tarball` (distinct from
    `deploy.source_ref`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateDeploymentFromSourceTarballBody):

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
