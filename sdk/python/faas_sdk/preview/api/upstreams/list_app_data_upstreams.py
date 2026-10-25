from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.data_upstream_list_response import DataUpstreamListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    deployment_scope: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["deployment_scope"] = deployment_scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/upstreams".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DataUpstreamListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DataUpstreamListResponse.from_dict(response.json())

        return response_200

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
) -> Response[DataUpstreamListResponse | Problem]:
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
    deployment_scope: str | Unset = UNSET,
) -> Response[DataUpstreamListResponse | Problem]:
    r"""List data upstreams on an app.

     Returns every captured (host_redacted_hash, kind, port) tuple
    on the app. The plaintext host NEVER appears in the response —
    only the SHA-256 hash of (salt||host). See spec §11.

    The list is bounded by the per-plan `api.DataPlacementHintsPerApp`
    quota (Free=0, Hobby=3, Pro=10, Scale=50). When FAAS_DATA_PLACEMENT
    is on the classifier derives entries on env mutation; when OFF
    the table stays empty and the response is `[]`.

    ADR-098 amendment (issue #954): `?deployment_scope=` narrows
    the list to one deployment so the dashboard can render
    staging-vs-prod independently. Omitted = \"all deployments\".

    Args:
        slug (str):
        deployment_scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataUpstreamListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_scope=deployment_scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_scope: str | Unset = UNSET,
) -> DataUpstreamListResponse | Problem | None:
    r"""List data upstreams on an app.

     Returns every captured (host_redacted_hash, kind, port) tuple
    on the app. The plaintext host NEVER appears in the response —
    only the SHA-256 hash of (salt||host). See spec §11.

    The list is bounded by the per-plan `api.DataPlacementHintsPerApp`
    quota (Free=0, Hobby=3, Pro=10, Scale=50). When FAAS_DATA_PLACEMENT
    is on the classifier derives entries on env mutation; when OFF
    the table stays empty and the response is `[]`.

    ADR-098 amendment (issue #954): `?deployment_scope=` narrows
    the list to one deployment so the dashboard can render
    staging-vs-prod independently. Omitted = \"all deployments\".

    Args:
        slug (str):
        deployment_scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataUpstreamListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        deployment_scope=deployment_scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_scope: str | Unset = UNSET,
) -> Response[DataUpstreamListResponse | Problem]:
    r"""List data upstreams on an app.

     Returns every captured (host_redacted_hash, kind, port) tuple
    on the app. The plaintext host NEVER appears in the response —
    only the SHA-256 hash of (salt||host). See spec §11.

    The list is bounded by the per-plan `api.DataPlacementHintsPerApp`
    quota (Free=0, Hobby=3, Pro=10, Scale=50). When FAAS_DATA_PLACEMENT
    is on the classifier derives entries on env mutation; when OFF
    the table stays empty and the response is `[]`.

    ADR-098 amendment (issue #954): `?deployment_scope=` narrows
    the list to one deployment so the dashboard can render
    staging-vs-prod independently. Omitted = \"all deployments\".

    Args:
        slug (str):
        deployment_scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataUpstreamListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_scope=deployment_scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_scope: str | Unset = UNSET,
) -> DataUpstreamListResponse | Problem | None:
    r"""List data upstreams on an app.

     Returns every captured (host_redacted_hash, kind, port) tuple
    on the app. The plaintext host NEVER appears in the response —
    only the SHA-256 hash of (salt||host). See spec §11.

    The list is bounded by the per-plan `api.DataPlacementHintsPerApp`
    quota (Free=0, Hobby=3, Pro=10, Scale=50). When FAAS_DATA_PLACEMENT
    is on the classifier derives entries on env mutation; when OFF
    the table stays empty and the response is `[]`.

    ADR-098 amendment (issue #954): `?deployment_scope=` narrows
    the list to one deployment so the dashboard can render
    staging-vs-prod independently. Omitted = \"all deployments\".

    Args:
        slug (str):
        deployment_scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataUpstreamListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            deployment_scope=deployment_scope,
        )
    ).parsed
