// Domestique service worker. Built by vite.config.ts with the precache list.
// The app shell works with no connection; data lives in IndexedDB, not here.
const VERSION = __VERSION__;
const PRECACHE = __PRECACHE__;
const CACHE = `domestique-shell-${VERSION}`;
// redirect: 'error' on precache: if the Outpost sign-in has lapsed, the gateway
// answers with a redirect to its login page, which must never be cached as an
// app file. The install fails instead and is retried on the next visit.

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((c) => c.addAll(PRECACHE.map((u) => new Request(u, { credentials: 'same-origin', cache: 'reload', redirect: 'error' }))))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith('domestique-shell-') && k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  // API calls, the healthcheck and the gateway's own paths always hit the network.
  if (url.pathname.startsWith('/v1/') || url.pathname === '/up' || url.pathname.startsWith('/.outpost/')) return;

  if (req.mode === 'navigate') {
    // Network first, so an expired gateway session can redirect to sign-in;
    // the cached shell when there's no connection at all.
    // A basement gym often has one bar of signal: give up on the network
    // after 3.5s rather than hang on a white screen.
    const shell = () => caches.match('/', { cacheName: CACHE }).then((r) => r || Response.error());
    const network = fetch(req);
    const timeout = new Promise((resolve) => setTimeout(resolve, 3500)).then(shell);
    event.respondWith(Promise.race([network.catch(shell), timeout]));
    return;
  }

  event.respondWith(
    caches.match(req, { cacheName: CACHE }).then((hit) => hit || fetch(req)),
  );
});
