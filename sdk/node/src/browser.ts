// Browser-safe entry point. Keep this barrel free of imports from the Node
// SDK's root surface, which intentionally includes Node-only helpers.
export {
  createGregaleBrowserFetch,
  GREGALE_RELEASE_HEADER,
  GREGALE_RELEASE_COOKIE,
  GREGALE_REVISION_HEADER,
  type GregaleBrowserFetchClient,
  type GregaleBrowserFetchOptions,
} from './browser-release.js';
