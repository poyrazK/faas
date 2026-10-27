// Browser-safe GA and beta entry point ('@gregale/sdk-node/browser',
// ADR-20261023115741). The realtime and operations browser helpers are
// preview capabilities: '@gregale/sdk-node/preview/browser' (src/browser.ts).
export {
  createGregaleBrowserFetch,
  GREGALE_RELEASE_HEADER,
  GREGALE_RELEASE_COOKIE,
  GREGALE_RELEASE_SUBPROTOCOL_PREFIX,
  GREGALE_REVISION_HEADER,
  type GregaleBrowserFetchClient,
  type GregaleBrowserFetchOptions,
} from './browser-release.js';
