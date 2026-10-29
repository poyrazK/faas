from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_promotion_preview_response import ProjectEnvironmentPromotionPreviewResponse
from ...models.project_environment_promotion_secret_preview_request import (
    ProjectEnvironmentPromotionSecretPreviewRequest,
)
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    body: ProjectEnvironmentPromotionSecretPreviewRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/{slug}/environments/{environment}/promotion-preview".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentPromotionPreviewResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentPromotionPreviewResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectEnvironmentPromotionPreviewResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectEnvironmentPromotionSecretPreviewRequest,
) -> Response[Problem | ProjectEnvironmentPromotionPreviewResponse]:
    """Preview promotion with destination production secret changes.

     Read-only, bounded preview containing immutable secret version references
    from the authenticated release-secrets staging endpoint. Requires MFA,
    secrets write permission, sync_config, verify, complete HTTP release graphs
    and production as the destination. Existing application-owned persistent
    secret keys only; workers, migrations, managed credentials, explicit grants
    and reload hooks are refused. Each mentioned workload supplies a complete
    secret map; an empty map removes its serving secrets and omitted workloads
    inherit production versions. Source secrets are never copied. Approval and
    execution revalidate the exact versions and destination baseline. New
    admissions also require qualified operator rollout for the secrets scope.

    Args:
        slug (str):
        environment (str):
        body (ProjectEnvironmentPromotionSecretPreviewRequest): Environment promotion preview
            inputs referencing staged immutable secrets without plaintext values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionPreviewResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectEnvironmentPromotionSecretPreviewRequest,
) -> Problem | ProjectEnvironmentPromotionPreviewResponse | None:
    """Preview promotion with destination production secret changes.

     Read-only, bounded preview containing immutable secret version references
    from the authenticated release-secrets staging endpoint. Requires MFA,
    secrets write permission, sync_config, verify, complete HTTP release graphs
    and production as the destination. Existing application-owned persistent
    secret keys only; workers, migrations, managed credentials, explicit grants
    and reload hooks are refused. Each mentioned workload supplies a complete
    secret map; an empty map removes its serving secrets and omitted workloads
    inherit production versions. Source secrets are never copied. Approval and
    execution revalidate the exact versions and destination baseline. New
    admissions also require qualified operator rollout for the secrets scope.

    Args:
        slug (str):
        environment (str):
        body (ProjectEnvironmentPromotionSecretPreviewRequest): Environment promotion preview
            inputs referencing staged immutable secrets without plaintext values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionPreviewResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectEnvironmentPromotionSecretPreviewRequest,
) -> Response[Problem | ProjectEnvironmentPromotionPreviewResponse]:
    """Preview promotion with destination production secret changes.

     Read-only, bounded preview containing immutable secret version references
    from the authenticated release-secrets staging endpoint. Requires MFA,
    secrets write permission, sync_config, verify, complete HTTP release graphs
    and production as the destination. Existing application-owned persistent
    secret keys only; workers, migrations, managed credentials, explicit grants
    and reload hooks are refused. Each mentioned workload supplies a complete
    secret map; an empty map removes its serving secrets and omitted workloads
    inherit production versions. Source secrets are never copied. Approval and
    execution revalidate the exact versions and destination baseline. New
    admissions also require qualified operator rollout for the secrets scope.

    Args:
        slug (str):
        environment (str):
        body (ProjectEnvironmentPromotionSecretPreviewRequest): Environment promotion preview
            inputs referencing staged immutable secrets without plaintext values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionPreviewResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectEnvironmentPromotionSecretPreviewRequest,
) -> Problem | ProjectEnvironmentPromotionPreviewResponse | None:
    """Preview promotion with destination production secret changes.

     Read-only, bounded preview containing immutable secret version references
    from the authenticated release-secrets staging endpoint. Requires MFA,
    secrets write permission, sync_config, verify, complete HTTP release graphs
    and production as the destination. Existing application-owned persistent
    secret keys only; workers, migrations, managed credentials, explicit grants
    and reload hooks are refused. Each mentioned workload supplies a complete
    secret map; an empty map removes its serving secrets and omitted workloads
    inherit production versions. Source secrets are never copied. Approval and
    execution revalidate the exact versions and destination baseline. New
    admissions also require qualified operator rollout for the secrets scope.

    Args:
        slug (str):
        environment (str):
        body (ProjectEnvironmentPromotionSecretPreviewRequest): Environment promotion preview
            inputs referencing staged immutable secrets without plaintext values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentPromotionPreviewResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            client=client,
            body=body,
        )
    ).parsed
