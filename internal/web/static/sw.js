// Wishbone's service worker.
//
// It exists to make the app installable and to make the shell load fast on a
// phone. It deliberately caches **only** /static/ — stylesheet, script, icons.
//
// Nothing authenticated is ever cached. A wishlist page or an item picture
// sitting in a service worker cache on a shared family phone is a privacy
// problem, not a performance win: someone else picking up the device could
// read a list they were never shared, and a cached page could show a claim
// after it was released. Every other request goes straight to the network.

const CACHE = "wishbone-static-v1";

const SHELL = [
  "/static/app.css",
  "/static/app.js",
  "/static/htmx.min.js",
  "/static/icon.svg",
  "/static/icon-192.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.addAll(SHELL))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))),
      )
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);

  // Anything that is not a same-origin GET for a static asset is left entirely
  // alone — no interception, no caching, no offline fallback.
  if (
    event.request.method !== "GET" ||
    url.origin !== self.location.origin ||
    !url.pathname.startsWith("/static/")
  ) {
    return;
  }

  event.respondWith(
    caches.match(event.request).then((hit) => hit || fetch(event.request)),
  );
});
