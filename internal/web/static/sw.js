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

// The cache is keyed on the build version, which app.js passes in the query of
// the script URL. That matters more than it looks: this worker is registered
// once and then only replaced when its own bytes change, so a hand-written
// version constant is a thing somebody has to remember, and the release that
// changed every icon in the app is the release that proved nobody does. A new
// build is a new script URL, which is a new worker, a new cache, and the old
// cache deleted on activate.
const VERSION = new URL(self.location.href).searchParams.get("v") || "dev";
const CACHE = "wishbone-static-" + VERSION;

// Only what the shell needs in order to render. Icons are deliberately absent:
// they are fetched rarely, they are not on the critical path, and precaching
// them is what pinned a stale set of them into every installed client.
const SHELL = ["/static/app.css", "/static/app.js", "/static/htmx.min.js"];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) =>
        // cache: "reload" so the HTTP cache cannot satisfy these from the
        // previous build. Without it, a worker installed to pick up new assets
        // can populate itself with the old ones and be none the wiser.
        cache.addAll(
          SHELL.map(
            (path) =>
              new Request(path + "?v=" + encodeURIComponent(VERSION), {
                cache: "reload",
              }),
          ),
        ),
      )
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

  // Stale while revalidate: answer from the cache when there is a hit, and
  // refresh the entry in the background either way. The version in the URL is
  // the mechanism that retires old files, and this is the safety net for when
  // it is not enough — an asset changed without a release, a worker that
  // installed against a stale HTTP cache — so a wrong entry costs one page load
  // instead of living forever.
  event.respondWith(
    caches.open(CACHE).then((cache) =>
      cache.match(event.request).then((hit) => {
        const fresh = fetch(event.request)
          .then((res) => {
            if (res && res.ok) {
              cache.put(event.request, res.clone());
            }
            return res;
          })
          .catch(() => hit);
        return hit || fresh;
      }),
    ),
  );
});
