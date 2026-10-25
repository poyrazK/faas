from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.org_response import OrgResponse
from ...models.patch_org_request import PatchOrgRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: PatchOrgRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/orgs/{slug}".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> OrgResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OrgResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[OrgResponse | Problem]:
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
    body: PatchOrgRequest,
) -> Response[OrgResponse | Problem]:
    r"""Update the org (name and/or plan).

     Partial update; both fields are pointer-typed so the
    handler distinguishes \"omitted\" from \"clear\". Authz
    routing:
      - `name` → `org.manage_billing` (owner + billing)
      - `plan` → `org.change_plan` (owner only)
    Personal orgs are immutable (`org_personal_immutable` 409).

    Args:
        slug (str):
        body (PatchOrgRequest): PATCH /v1/orgs/{slug} body. Both fields are pointer-typed
            so the handler distinguishes "omitted" (leave alone) from
            "zero" (clear/empty). Authz routing:
              - name → org.manage_billing (owner + billing roles)
              - plan → org.change_plan (owner only)

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgResponse | Problem]
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
    body: PatchOrgRequest,
) -> OrgResponse | Problem | None:
    r"""Update the org (name and/or plan).

     Partial update; both fields are pointer-typed so the
    handler distinguishes \"omitted\" from \"clear\". Authz
    routing:
      - `name` → `org.manage_billing` (owner + billing)
      - `plan` → `org.change_plan` (owner only)
    Personal orgs are immutable (`org_personal_immutable` 409).

    Args:
        slug (str):
        body (PatchOrgRequest): PATCH /v1/orgs/{slug} body. Both fields are pointer-typed
            so the handler distinguishes "omitted" (leave alone) from
            "zero" (clear/empty). Authz routing:
              - name → org.manage_billing (owner + billing roles)
              - plan → org.change_plan (owner only)

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgResponse | Problem
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
    body: PatchOrgRequest,
) -> Response[OrgResponse | Problem]:
    r"""Update the org (name and/or plan).

     Partial update; both fields are pointer-typed so the
    handler distinguishes \"omitted\" from \"clear\". Authz
    routing:
      - `name` → `org.manage_billing` (owner + billing)
      - `plan` → `org.change_plan` (owner only)
    Personal orgs are immutable (`org_personal_immutable` 409).

    Args:
        slug (str):
        body (PatchOrgRequest): PATCH /v1/orgs/{slug} body. Both fields are pointer-typed
            so the handler distinguishes "omitted" (leave alone) from
            "zero" (clear/empty). Authz routing:
              - name → org.manage_billing (owner + billing roles)
              - plan → org.change_plan (owner only)

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OrgResponse | Problem]
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
    body: PatchOrgRequest,
) -> OrgResponse | Problem | None:
    r"""Update the org (name and/or plan).

     Partial update; both fields are pointer-typed so the
    handler distinguishes \"omitted\" from \"clear\". Authz
    routing:
      - `name` → `org.manage_billing` (owner + billing)
      - `plan` → `org.change_plan` (owner only)
    Personal orgs are immutable (`org_personal_immutable` 409).

    Args:
        slug (str):
        body (PatchOrgRequest): PATCH /v1/orgs/{slug} body. Both fields are pointer-typed
            so the handler distinguishes "omitted" (leave alone) from
            "zero" (clear/empty). Authz routing:
              - name → org.manage_billing (owner + billing roles)
              - plan → org.change_plan (owner only)

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OrgResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
