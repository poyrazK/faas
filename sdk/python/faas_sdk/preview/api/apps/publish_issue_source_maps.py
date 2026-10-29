from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_source_maps_metadata import IssueSourceMapsMetadata
from ...models.problem import Problem
from ...models.publish_issue_source_maps_request import PublishIssueSourceMapsRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment_id: UUID,
    *,
    body: PublishIssueSourceMapsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/deployments/{deployment_id}/issue-source-maps".format(
            slug=quote(str(slug), safe=""),
            deployment_id=quote(str(deployment_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | IssueSourceMapsMetadata | Problem | None:
    if response.status_code == 200:
        response_200 = IssueSourceMapsMetadata.from_dict(response.json())

        return response_200

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
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | IssueSourceMapsMetadata | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient,
    body: PublishIssueSourceMapsRequest,
) -> Response[Any | IssueSourceMapsMetadata | Problem]:
    """Publish immutable deployment-local source-map artifacts.

     Requires deploy-write permission and session MFA. Source contents and extensions are discarded.
    Matching canonical manifests are idempotent; a different manifest for the deployment conflicts,
    including after retirement.

    Args:
        slug (str):
        deployment_id (UUID):
        body (PublishIssueSourceMapsRequest): Deployment source-map publication request containing
            the deployed directory and portable manifest.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IssueSourceMapsMetadata | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient,
    body: PublishIssueSourceMapsRequest,
) -> Any | IssueSourceMapsMetadata | Problem | None:
    """Publish immutable deployment-local source-map artifacts.

     Requires deploy-write permission and session MFA. Source contents and extensions are discarded.
    Matching canonical manifests are idempotent; a different manifest for the deployment conflicts,
    including after retirement.

    Args:
        slug (str):
        deployment_id (UUID):
        body (PublishIssueSourceMapsRequest): Deployment source-map publication request containing
            the deployed directory and portable manifest.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IssueSourceMapsMetadata | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment_id=deployment_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient,
    body: PublishIssueSourceMapsRequest,
) -> Response[Any | IssueSourceMapsMetadata | Problem]:
    """Publish immutable deployment-local source-map artifacts.

     Requires deploy-write permission and session MFA. Source contents and extensions are discarded.
    Matching canonical manifests are idempotent; a different manifest for the deployment conflicts,
    including after retirement.

    Args:
        slug (str):
        deployment_id (UUID):
        body (PublishIssueSourceMapsRequest): Deployment source-map publication request containing
            the deployed directory and portable manifest.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IssueSourceMapsMetadata | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment_id: UUID,
    *,
    client: AuthenticatedClient,
    body: PublishIssueSourceMapsRequest,
) -> Any | IssueSourceMapsMetadata | Problem | None:
    """Publish immutable deployment-local source-map artifacts.

     Requires deploy-write permission and session MFA. Source contents and extensions are discarded.
    Matching canonical manifests are idempotent; a different manifest for the deployment conflicts,
    including after retirement.

    Args:
        slug (str):
        deployment_id (UUID):
        body (PublishIssueSourceMapsRequest): Deployment source-map publication request containing
            the deployed directory and portable manifest.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IssueSourceMapsMetadata | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment_id=deployment_id,
            client=client,
            body=body,
        )
    ).parsed
