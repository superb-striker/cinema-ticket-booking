const VERSION = "cinema-pwa-v3";
const SHELL = [
  "/",
  "/app.js?v=priority1-customer-4",
  "/manifest.webmanifest",
  "/icon.svg",
  "/scanner.html",
  "/scanner.js?v=1",
];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(VERSION).then((cache) => cache.addAll(SHELL)));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((key) => key !== VERSION).map((key) => caches.delete(key))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin || url.pathname.endsWith("/seat-events")) return;

  if (request.mode === "navigate") {
    event.respondWith(fetch(request).catch(() => caches.match("/")));
    return;
  }

  if (isPublicCatalogRequest(url)) {
    event.respondWith(networkFirst(request));
    return;
  }

  if (!url.pathname.startsWith("/api/")) {
    event.respondWith(cacheFirst(request));
  }
});

function isPublicCatalogRequest(url) {
  return url.pathname === "/api/v1/screenings" || /^\/api\/v1\/screenings\/[^/]+\/seats$/.test(url.pathname);
}

async function networkFirst(request) {
  const cache = await caches.open(VERSION);
  try {
    const response = await fetch(request);
    if (response.ok) await cache.put(request, response.clone());
    return response;
  } catch (error) {
    const cached = await cache.match(request);
    if (cached) return cached;
    throw error;
  }
}

async function cacheFirst(request) {
  const cache = await caches.open(VERSION);
  const cached = await cache.match(request);
  if (cached) {
    fetch(request).then((response) => {
      if (response.ok) cache.put(request, response);
    }).catch(() => {});
    return cached;
  }
  const response = await fetch(request);
  if (response.ok) await cache.put(request, response.clone());
  return response;
}
