// HTTP shim over get-product-name, implementing the contract in ../README.md.
// Deliberately tiny: it is a translation layer, not a service.
//
// The library is MIT-licensed and returns { name, price, image } — a free-text
// price string, not a number. Parsing that into cents is the Go side's job;
// this shim passes it through untouched rather than guessing.

'use strict';

const http = require('node:http');

const PORT = Number(process.env.PORT || 8081);
const HOST = process.env.HOST || '127.0.0.1';
const TIMEOUT_MS = Number(process.env.EXTRACT_TIMEOUT_MS || 9000);

let getProductData = null;
try {
	// The npm package is "get-product-name"; the GitHub repository behind it is
	// named get-product-data. Both names refer to the same library.
	// eslint-disable-next-line global-require
	getProductData = require('get-product-name');
} catch (err) {
	console.error('[extractor] get-product-name is not installed:', err.message);
}

function send(res, status, payload) {
	const body = JSON.stringify(payload);
	res.writeHead(status, {
		'content-type': 'application/json; charset=utf-8',
		'content-length': Buffer.byteLength(body),
	});
	res.end(body);
}

// normalize maps the library's shape onto the contract. It returns
// { name, price, image }; the extra fields in the contract are optional and
// simply stay empty, which the Go client handles by leaving those fields to
// other tiers or to the person filling in the form.
function normalize(raw) {
	const first = Array.isArray(raw) ? raw[0] : raw;
	if (!first || typeof first !== 'object') return {};

	const images = []
		.concat(first.images || [], first.image || [])
		.filter((v) => typeof v === 'string' && v.length > 0);

	return {
		title: first.name || first.title || '',
		description: first.description || '',
		// Free text, e.g. "$39.99" or "Currently unavailable." The Go side
		// parses what it can and ignores what it cannot.
		price: first.price != null ? String(first.price) : '',
		currency: first.currency || '',
		images,
		sku: first.sku || first.asin || '',
		brand: first.brand || '',
		attributes: first.attributes && typeof first.attributes === 'object' ? first.attributes : {},
	};
}

const server = http.createServer(async (req, res) => {
	const url = new URL(req.url, `http://${req.headers.host || 'localhost'}`);

	if (url.pathname === '/healthz') {
		send(res, 200, { ok: true, library: Boolean(getProductData) });
		return;
	}
	if (url.pathname !== '/extract') {
		send(res, 404, { error: 'not found' });
		return;
	}

	const target = url.searchParams.get('url');
	if (!target) {
		send(res, 400, { error: 'missing url parameter' });
		return;
	}
	if (!/^https?:\/\//i.test(target)) {
		send(res, 400, { error: 'only http and https URLs are supported' });
		return;
	}
	if (!getProductData) {
		send(res, 503, { error: 'extractor library unavailable' });
		return;
	}

	try {
		// The library takes (url, proxy, timeout) and enforces the timeout
		// itself; the Go client applies its own on top.
		const raw = await getProductData(target, undefined, TIMEOUT_MS);
		send(res, 200, normalize(raw));
	} catch (err) {
		// A failure is reported as a 200 with an error field: the Go client
		// treats both the same way, and this keeps a scrape miss out of the
		// container's error logs.
		send(res, 200, { error: String(err && err.message ? err.message : err) });
	}
});

server.listen(PORT, HOST, () => {
	console.log(`[extractor] listening on http://${HOST}:${PORT}`);
});
