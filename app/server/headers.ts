import type { RequestHandler } from 'express';

// CSP: self only, plus OSM map tiles. style-src allows unsafe-inline for now
// (see frontend-spec); tightening via nonce is tier-2.
//
// img-src additionally allows https://tile.openstreetmap.org because the map
// view (Leaflet) loads OSM raster tiles as <img>. This is the only off-origin
// resource the SPA fetches: tiles are images, so script-src/connect-src stay
// 'self'. To keep the panel fully self-contained (air-gap / no off-origin
// requests), proxy tiles through this server instead and drop this entry.
const CSP =
  "default-src 'self'; " +
  "img-src 'self' data: https://tile.openstreetmap.org; " +
  "script-src 'self'; " +
  "style-src 'self' 'unsafe-inline'; " +
  "connect-src 'self'; " +
  "frame-ancestors 'none'; " +
  "base-uri 'self'; " +
  "form-action 'self';";

export function securityHeaders(dev: boolean): RequestHandler {
  return (_req, res, next) => {
    res.setHeader('Content-Security-Policy', CSP);
    res.setHeader('X-Frame-Options', 'DENY');
    res.setHeader('X-Content-Type-Options', 'nosniff');
    res.setHeader('Referrer-Policy', 'no-referrer');
    res.setHeader('Permissions-Policy', '');
    if (!dev) {
      res.setHeader('Strict-Transport-Security', 'max-age=31536000; includeSubDomains');
    }
    next();
  };
}
