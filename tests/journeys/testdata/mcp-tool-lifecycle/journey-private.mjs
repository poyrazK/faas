// Synthetic staging issuer: public verification keys only. The signing key and
// control token remain on the journey host. Normal application JWT verification
// still enforces signature, issuer, audience, expiry and scopes.
import { createAuth } from './auth.js';

export function installPrivateJourneyControls(runtime, config, keys, jwks) {
  const auth = createAuth(config, keys);
  runtime.app.get('/journey/jwks', (_req, res) => res.json(jwks));
  const metadata = { issuer: config.auth.issuer, jwks_uri: config.auth.jwks_url };
  runtime.app.get('/journey/issuer/.well-known/openid-configuration', (_req, res) => res.json(metadata));
  runtime.app.post('/journey/catalog-drift', auth.middleware, (req, res) => {
    if (!req.auth.scopes.includes('journey:control')) return res.status(403).end();
    // Removing the real registration changes the actual catalog revision/hash.
    runtime.removeTool('journey_echo');
    return res.status(204).end();
  });
}
