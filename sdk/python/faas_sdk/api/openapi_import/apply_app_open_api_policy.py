from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_open_api_policy_apply_response import AppOpenAPIPolicyApplyResponse
from ...models.apply_app_open_api_policy_request import ApplyAppOpenAPIPolicyRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: ApplyAppOpenAPIPolicyRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/openapi/apply".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | AppOpenAPIPolicyApplyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppOpenAPIPolicyApplyResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 422:
        response_422 = cast(Any, None)
        return response_422

    if response.status_code == 500:
        response_500 = cast(Any, None)
        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | AppOpenAPIPolicyApplyResponse | Problem]:
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
    body: ApplyAppOpenAPIPolicyRequest | Unset = UNSET,
) -> Response[Any | AppOpenAPIPolicyApplyResponse | Problem]:
    """Plan or explicitly apply generated OpenAPI validation rules.

     The default request is a read-only plan. It returns a deterministic
    `preview_sha256` approval token and the validation rules that would be
    created for uncovered OpenAPI operations. To mutate policy, repeat the
    request with `confirm=true` and the exact token. If the document or
    edge rules changed in the meantime, the server returns 409
    `openapi_policy_stale` and no writes occur. Applying an already-covered
    document is an idempotent no-op. A missing match_host defaults to the
    app's platform hostname. Requires MFA and deploy-write scope.

    Args:
        slug (str):
        body (ApplyAppOpenAPIPolicyRequest | Unset): Controls the explicit OpenAPI policy
            plan/apply workflow. Omit the
            body (or set confirm=false) to request a read-only plan. A confirmed
            apply must include the preview_sha256 returned by that plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | AppOpenAPIPolicyApplyResponse | Problem]
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
    body: ApplyAppOpenAPIPolicyRequest | Unset = UNSET,
) -> Any | AppOpenAPIPolicyApplyResponse | Problem | None:
    """Plan or explicitly apply generated OpenAPI validation rules.

     The default request is a read-only plan. It returns a deterministic
    `preview_sha256` approval token and the validation rules that would be
    created for uncovered OpenAPI operations. To mutate policy, repeat the
    request with `confirm=true` and the exact token. If the document or
    edge rules changed in the meantime, the server returns 409
    `openapi_policy_stale` and no writes occur. Applying an already-covered
    document is an idempotent no-op. A missing match_host defaults to the
    app's platform hostname. Requires MFA and deploy-write scope.

    Args:
        slug (str):
        body (ApplyAppOpenAPIPolicyRequest | Unset): Controls the explicit OpenAPI policy
            plan/apply workflow. Omit the
            body (or set confirm=false) to request a read-only plan. A confirmed
            apply must include the preview_sha256 returned by that plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | AppOpenAPIPolicyApplyResponse | Problem
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
    body: ApplyAppOpenAPIPolicyRequest | Unset = UNSET,
) -> Response[Any | AppOpenAPIPolicyApplyResponse | Problem]:
    """Plan or explicitly apply generated OpenAPI validation rules.

     The default request is a read-only plan. It returns a deterministic
    `preview_sha256` approval token and the validation rules that would be
    created for uncovered OpenAPI operations. To mutate policy, repeat the
    request with `confirm=true` and the exact token. If the document or
    edge rules changed in the meantime, the server returns 409
    `openapi_policy_stale` and no writes occur. Applying an already-covered
    document is an idempotent no-op. A missing match_host defaults to the
    app's platform hostname. Requires MFA and deploy-write scope.

    Args:
        slug (str):
        body (ApplyAppOpenAPIPolicyRequest | Unset): Controls the explicit OpenAPI policy
            plan/apply workflow. Omit the
            body (or set confirm=false) to request a read-only plan. A confirmed
            apply must include the preview_sha256 returned by that plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | AppOpenAPIPolicyApplyResponse | Problem]
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
    body: ApplyAppOpenAPIPolicyRequest | Unset = UNSET,
) -> Any | AppOpenAPIPolicyApplyResponse | Problem | None:
    """Plan or explicitly apply generated OpenAPI validation rules.

     The default request is a read-only plan. It returns a deterministic
    `preview_sha256` approval token and the validation rules that would be
    created for uncovered OpenAPI operations. To mutate policy, repeat the
    request with `confirm=true` and the exact token. If the document or
    edge rules changed in the meantime, the server returns 409
    `openapi_policy_stale` and no writes occur. Applying an already-covered
    document is an idempotent no-op. A missing match_host defaults to the
    app's platform hostname. Requires MFA and deploy-write scope.

    Args:
        slug (str):
        body (ApplyAppOpenAPIPolicyRequest | Unset): Controls the explicit OpenAPI policy
            plan/apply workflow. Omit the
            body (or set confirm=false) to request a read-only plan. A confirmed
            apply must include the preview_sha256 returned by that plan.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | AppOpenAPIPolicyApplyResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
