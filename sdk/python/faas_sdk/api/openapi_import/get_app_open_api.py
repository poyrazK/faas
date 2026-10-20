from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_app_open_api_response_200 import GetAppOpenAPIResponse200
from ...models.get_app_open_api_source import GetAppOpenAPISource
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    source: GetAppOpenAPISource | Unset = "manual_import",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_source: str | Unset = UNSET
    if not isinstance(source, Unset):
        json_source = source

    params["source"] = json_source

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/openapi".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | GetAppOpenAPIResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = GetAppOpenAPIResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 405:
        response_405 = cast(Any, None)
        return response_405

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | GetAppOpenAPIResponse200 | Problem]:
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
    source: GetAppOpenAPISource | Unset = "manual_import",
) -> Response[Any | GetAppOpenAPIResponse200 | Problem]:
    r"""Read the imported (or auto-generated) OpenAPI document for an app.

     Two source modes (selected via `?source=`):

    - `manual_import` (default): returns the customer's uploaded
      doc verbatim. Mirrors item #1's /deployments/{dep}/openapi
      but on the app-keyed `app_openapi_docs` table (ADR-126 D1).

    - `auto`: runs pkg/openapidiff.GenerateFromApp with the
      imported doc + observed routes (ADR-093 bridge) +
      existing edge rules; the merged spec is cached for 5 min
      per (app_id, sha(doc), sha(routes), sha(rules)). Cache
      headers: X-Faas-Cache: hit|miss, X-OpenAPI-Doc-Source:
      \"auto\" | \"degraded: routes_unavailable\" |
      \"degraded: rules_unavailable\" | \"empty: no_import_no_rules\".

    Limits are abuse-surface, not plan-tier — every plan
    including Free can import. Per-account row cap is
    Plan.OpenAPIImportsPerAccount (Free 100, Hobby 1000,
    Pro 10000, Scale 10000). Plan-tier gate is intentionally
    absent on this surface (ADR-126 D6).

    Args:
        slug (str):
        source (GetAppOpenAPISource | Unset):  Default: 'manual_import'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | GetAppOpenAPIResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        source=source,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    source: GetAppOpenAPISource | Unset = "manual_import",
) -> Any | GetAppOpenAPIResponse200 | Problem | None:
    r"""Read the imported (or auto-generated) OpenAPI document for an app.

     Two source modes (selected via `?source=`):

    - `manual_import` (default): returns the customer's uploaded
      doc verbatim. Mirrors item #1's /deployments/{dep}/openapi
      but on the app-keyed `app_openapi_docs` table (ADR-126 D1).

    - `auto`: runs pkg/openapidiff.GenerateFromApp with the
      imported doc + observed routes (ADR-093 bridge) +
      existing edge rules; the merged spec is cached for 5 min
      per (app_id, sha(doc), sha(routes), sha(rules)). Cache
      headers: X-Faas-Cache: hit|miss, X-OpenAPI-Doc-Source:
      \"auto\" | \"degraded: routes_unavailable\" |
      \"degraded: rules_unavailable\" | \"empty: no_import_no_rules\".

    Limits are abuse-surface, not plan-tier — every plan
    including Free can import. Per-account row cap is
    Plan.OpenAPIImportsPerAccount (Free 100, Hobby 1000,
    Pro 10000, Scale 10000). Plan-tier gate is intentionally
    absent on this surface (ADR-126 D6).

    Args:
        slug (str):
        source (GetAppOpenAPISource | Unset):  Default: 'manual_import'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | GetAppOpenAPIResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        source=source,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    source: GetAppOpenAPISource | Unset = "manual_import",
) -> Response[Any | GetAppOpenAPIResponse200 | Problem]:
    r"""Read the imported (or auto-generated) OpenAPI document for an app.

     Two source modes (selected via `?source=`):

    - `manual_import` (default): returns the customer's uploaded
      doc verbatim. Mirrors item #1's /deployments/{dep}/openapi
      but on the app-keyed `app_openapi_docs` table (ADR-126 D1).

    - `auto`: runs pkg/openapidiff.GenerateFromApp with the
      imported doc + observed routes (ADR-093 bridge) +
      existing edge rules; the merged spec is cached for 5 min
      per (app_id, sha(doc), sha(routes), sha(rules)). Cache
      headers: X-Faas-Cache: hit|miss, X-OpenAPI-Doc-Source:
      \"auto\" | \"degraded: routes_unavailable\" |
      \"degraded: rules_unavailable\" | \"empty: no_import_no_rules\".

    Limits are abuse-surface, not plan-tier — every plan
    including Free can import. Per-account row cap is
    Plan.OpenAPIImportsPerAccount (Free 100, Hobby 1000,
    Pro 10000, Scale 10000). Plan-tier gate is intentionally
    absent on this surface (ADR-126 D6).

    Args:
        slug (str):
        source (GetAppOpenAPISource | Unset):  Default: 'manual_import'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | GetAppOpenAPIResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        source=source,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    source: GetAppOpenAPISource | Unset = "manual_import",
) -> Any | GetAppOpenAPIResponse200 | Problem | None:
    r"""Read the imported (or auto-generated) OpenAPI document for an app.

     Two source modes (selected via `?source=`):

    - `manual_import` (default): returns the customer's uploaded
      doc verbatim. Mirrors item #1's /deployments/{dep}/openapi
      but on the app-keyed `app_openapi_docs` table (ADR-126 D1).

    - `auto`: runs pkg/openapidiff.GenerateFromApp with the
      imported doc + observed routes (ADR-093 bridge) +
      existing edge rules; the merged spec is cached for 5 min
      per (app_id, sha(doc), sha(routes), sha(rules)). Cache
      headers: X-Faas-Cache: hit|miss, X-OpenAPI-Doc-Source:
      \"auto\" | \"degraded: routes_unavailable\" |
      \"degraded: rules_unavailable\" | \"empty: no_import_no_rules\".

    Limits are abuse-surface, not plan-tier — every plan
    including Free can import. Per-account row cap is
    Plan.OpenAPIImportsPerAccount (Free 100, Hobby 1000,
    Pro 10000, Scale 10000). Plan-tier gate is intentionally
    absent on this surface (ADR-126 D6).

    Args:
        slug (str):
        source (GetAppOpenAPISource | Unset):  Default: 'manual_import'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | GetAppOpenAPIResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            source=source,
        )
    ).parsed
