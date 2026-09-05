from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deploy_dev_source_body import DeployDevSourceBody
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: DeployDevSourceBody,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments/dev-source".format(
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
    body: DeployDevSourceBody,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Create a developer deployment from a complete source snapshot or delta.

     Transport used only for ad-hoc developer environments created by
    `gregale dev`. With an empty `dev_source_base`, `source` is a complete
    source archive. With a base revision, `source` contains changed entries
    and `dev_source_deleted` removes paths from the cached base.

    The cache is account/app scoped, node-local, and disposable. apid
    reconstructs and verifies a complete archive before applying the same
    source-root, stateful-shape, secret-scan, Dockerfile, function, and
    enqueue gates as an ordinary source deployment. A missing base returns
    409 `dev_source_base_missing`; clients retry the target as a complete
    snapshot. Older servers safely return 404 on this distinct route.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (DeployDevSourceBody):

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
    body: DeployDevSourceBody,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Create a developer deployment from a complete source snapshot or delta.

     Transport used only for ad-hoc developer environments created by
    `gregale dev`. With an empty `dev_source_base`, `source` is a complete
    source archive. With a base revision, `source` contains changed entries
    and `dev_source_deleted` removes paths from the cached base.

    The cache is account/app scoped, node-local, and disposable. apid
    reconstructs and verifies a complete archive before applying the same
    source-root, stateful-shape, secret-scan, Dockerfile, function, and
    enqueue gates as an ordinary source deployment. A missing base returns
    409 `dev_source_base_missing`; clients retry the target as a complete
    snapshot. Older servers safely return 404 on this distinct route.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (DeployDevSourceBody):

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
    body: DeployDevSourceBody,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Create a developer deployment from a complete source snapshot or delta.

     Transport used only for ad-hoc developer environments created by
    `gregale dev`. With an empty `dev_source_base`, `source` is a complete
    source archive. With a base revision, `source` contains changed entries
    and `dev_source_deleted` removes paths from the cached base.

    The cache is account/app scoped, node-local, and disposable. apid
    reconstructs and verifies a complete archive before applying the same
    source-root, stateful-shape, secret-scan, Dockerfile, function, and
    enqueue gates as an ordinary source deployment. A missing base returns
    409 `dev_source_base_missing`; clients retry the target as a complete
    snapshot. Older servers safely return 404 on this distinct route.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (DeployDevSourceBody):

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
    body: DeployDevSourceBody,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Create a developer deployment from a complete source snapshot or delta.

     Transport used only for ad-hoc developer environments created by
    `gregale dev`. With an empty `dev_source_base`, `source` is a complete
    source archive. With a base revision, `source` contains changed entries
    and `dev_source_deleted` removes paths from the cached base.

    The cache is account/app scoped, node-local, and disposable. apid
    reconstructs and verifies a complete archive before applying the same
    source-root, stateful-shape, secret-scan, Dockerfile, function, and
    enqueue gates as an ordinary source deployment. A missing base returns
    409 `dev_source_base_missing`; clients retry the target as a complete
    snapshot. Older servers safely return 404 on this distinct route.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (DeployDevSourceBody):

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
