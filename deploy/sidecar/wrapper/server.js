// Reference HTTP shim over get-product-data, implementing the contract in
// ../README.md. Deliberately tiny: it is a translation layer, not a service.
//
// The library name is not pinned to a verified license yet (plan §12); see the
// README before shipping this.

'use strict';

const http = require('node:http');

const PORT = Number(process.env.PORT || 8081);
const HOST = process.env.HOST || '127.0.0.1';
const TIMEOUT_MS = Number(process.env.EXTRACT_TIMEOUT_MS || 9000);

let getProductData = null;
try {
	// eslint-disable-next-line global-require
	getProductData = require('get-product-data');
} catch (err) {
	console.error('[extractor] get-product-data is not installed:', err.message);
}

function send(res, status, payload) {
	const body = JSON.stringify(payload);
	res.writeHead(status, {
		'content-type': 'application/json; charset=utf-8',
		'content-length': Buffer.byteLength(body),
	});
	res.end(body);
}

// normalize maps whatever shape the library returns onto the contract. The Go
// side ignores unknown fields, so being generous here is free.
function normalize(raw) {
	const first = Array.isArray(raw) ? raw[0] : raw;
	if (!first || typeof first !== 'object') return {};

	const images = []
		.concat(first.images || [], first.image || [], first.imageUrl || [])
		.filter((v) => typeof v === 'string' && v.length > 0);

	return {
		title: first.title || first.name || '',
		description: first.description || '',
		price: first.price != null ? String(first.price) : '',
		currency: first.currency || first.priceCurrency || '',
		images,
		sku: first.sku || first.asin || '',
		brand: first.brand || first.vendor || '',
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

	const timer = setTimeout(() => {
		if (!res.headersSent) send(res, 504, { error: 'extraction timed out' });
	}, TIMEOUT_MS);

	try {
		const raw = await getProductData(target);
		clearTimeout(timer);
		if (!res.headersSent) send(res, 200, normalize(raw));
	} catch (err) {
		clearTimeout(timer);
		if (!res.headersSent) send(res, 200, { error: String(err && err.message ? err.message : err) });
	}
});

server.listen(PORT, HOST, () => {
	console.log(`[extractor] listening on http://${HOST}:${PORT}`);
});
