from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_promotion_preview_response import ProjectEnvironmentPromotionPreviewResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    environment: str,
    *,
    from_: str,
    sync_config: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["from"] = from_

    params["sync_config"] = sync_config

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/environments/{environment}/promotion-preview".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
        ),
        "params": params,
    }

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

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
    from_: str,
    sync_config: bool | Unset = False,
) -> Response[Problem | ProjectEnvironmentPromotionPreviewResponse]:
    """Preview promotion of live workloads between project environments.

     Read-only comparison of the source and target environment. The
    response includes non-secret configuration changes, live deployment
    identities, target protection state, and an opaque promotion token
    bound to those identities. It does not create deployments or audit
    mutations. Configuration remains target-scoped by default;
    `sync_config=true` opts into applying the source's non-secret
    configuration snapshot with the release graph, and is blocked unless
    the target already has an active release graph for atomic cutover.

    Args:
        slug (str):
        environment (str):
        from_ (str):
        sync_config (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionPreviewResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        from_=from_,
        sync_config=sync_config,
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
    from_: str,
    sync_config: bool | Unset = False,
) -> Problem | ProjectEnvironmentPromotionPreviewResponse | None:
    """Preview promotion of live workloads between project environments.

     Read-only comparison of the source and target environment. The
    response includes non-secret configuration changes, live deployment
    identities, target protection state, and an opaque promotion token
    bound to those identities. It does not create deployments or audit
    mutations. Configuration remains target-scoped by default;
    `sync_config=true` opts into applying the source's non-secret
    configuration snapshot with the release graph, and is blocked unless
    the target already has an active release graph for atomic cutover.

    Args:
        slug (str):
        environment (str):
        from_ (str):
        sync_config (bool | Unset):  Default: False.

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
        from_=from_,
        sync_config=sync_config,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    from_: str,
    sync_config: bool | Unset = False,
) -> Response[Problem | ProjectEnvironmentPromotionPreviewResponse]:
    """Preview promotion of live workloads between project environments.

     Read-only comparison of the source and target environment. The
    response includes non-secret configuration changes, live deployment
    identities, target protection state, and an opaque promotion token
    bound to those identities. It does not create deployments or audit
    mutations. Configuration remains target-scoped by default;
    `sync_config=true` opts into applying the source's non-secret
    configuration snapshot with the release graph, and is blocked unless
    the target already has an active release graph for atomic cutover.

    Args:
        slug (str):
        environment (str):
        from_ (str):
        sync_config (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentPromotionPreviewResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        from_=from_,
        sync_config=sync_config,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    *,
    client: AuthenticatedClient | Client,
    from_: str,
    sync_config: bool | Unset = False,
) -> Problem | ProjectEnvironmentPromotionPreviewResponse | None:
    """Preview promotion of live workloads between project environments.

     Read-only comparison of the source and target environment. The
    response includes non-secret configuration changes, live deployment
    identities, target protection state, and an opaque promotion token
    bound to those identities. It does not create deployments or audit
    mutations. Configuration remains target-scoped by default;
    `sync_config=true` opts into applying the source's non-secret
    configuration snapshot with the release graph, and is blocked unless
    the target already has an active release graph for atomic cutover.

    Args:
        slug (str):
        environment (str):
        from_ (str):
        sync_config (bool | Unset):  Default: False.

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
            from_=from_,
            sync_config=sync_config,
        )
    ).parsed
