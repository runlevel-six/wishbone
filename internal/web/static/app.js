// Wishbone's only script beyond htmx. Behavior is attached from data
// attributes rather than inline handlers so the CSP can forbid inline script.
(function () {
	"use strict";

	document.addEventListener("click", function (ev) {
		var open = ev.target.closest("[data-dialog-open]");
		if (open) {
			var dlg = document.getElementById(open.getAttribute("data-dialog-open"));
			if (dlg && typeof dlg.showModal === "function") {
				ev.preventDefault();
				dlg.showModal();
			}
			return;
		}
		var close = ev.target.closest("[data-dialog-close]");
		if (close) {
			var target = close.closest("dialog");
			if (target) {
				ev.preventDefault();
				target.close();
			}
		}
	});

	document.addEventListener("submit", function (ev) {
		var form = ev.target;
		var msg = form.getAttribute("data-confirm");
		if (msg && !window.confirm(msg)) {
			ev.preventDefault();
			ev.stopPropagation();
		}
	});

	// Same confirmation for htmx-issued requests.
	document.addEventListener("htmx:confirm", function (ev) {
		var msg = ev.detail.elt.getAttribute("data-confirm");
		if (!msg) { return; }
		ev.preventDefault();
		if (window.confirm(msg)) { ev.detail.issueRequest(); }
	});

	// Submit a select's form as soon as it changes, so choosing a sort order is
	// one action rather than two. The button stays in the markup and keeps
	// working: this only removes a step for people who have JavaScript.
	document.addEventListener("change", function (ev) {
		if (!ev.target.matches("select[data-autosubmit]")) { return; }
		var form = ev.target.form;
		if (form) { form.requestSubmit ? form.requestSubmit() : form.submit(); }
	});

	// Make a picture fit before it is sent. The server refuses a file over its
	// limit (data-shrink carries it), and phone photos often are over it. So a
	// photo that is too big, or in a format the server cannot read but this
	// browser can (HEIC on a Mac), is redrawn here at a sensible size and sent
	// as JPEG — PNG stays PNG while it fits, to keep a cutout's transparency.
	//
	// Anything that already fits is sent untouched: the server reads its EXIF
	// orientation and turns it upright itself, so there is no reason to trust
	// each browser's handling of that here. The redraw is through an <img>,
	// which every current browser draws the right way up, and through a data:
	// URL because the CSP allows those and not blob: URLs.
	//
	// Any failure leaves the original in place; the server then explains what
	// was wrong with it.
	var SHRINK_EDGE = 2048;
	var READABLE = /^image\/(jpeg|png|webp|gif)$/;

	document.addEventListener("change", function (ev) {
		var input = ev.target;
		if (!input.matches("input[type=file][data-shrink]")) { return; }
		var form = input.form;
		// Two inputs share the name "image"; keep only the one just used.
		if (form) {
			form.querySelectorAll("input[type=file][data-shrink]").forEach(function (other) {
				if (other !== input) { other.value = ""; }
			});
		}
		var file = input.files && input.files[0];
		var limit = parseInt(input.getAttribute("data-shrink"), 10) || 0;
		if (!file || (file.size <= limit && READABLE.test(file.type))) { return; }
		if (typeof DataTransfer !== "function" || !HTMLCanvasElement.prototype.toBlob) { return; }

		var buttons = form ? form.querySelectorAll("button[type=submit]") : [];
		buttons.forEach(function (b) { b.disabled = true; });
		var done = function () { buttons.forEach(function (b) { b.disabled = false; }); };

		var reader = new FileReader();
		reader.onerror = done;
		reader.onload = function () {
			var img = new Image();
			img.onerror = done;
			img.onload = function () {
				var w = img.naturalWidth, h = img.naturalHeight;
				var scale = Math.min(1, SHRINK_EDGE / Math.max(w, h));
				var canvas = document.createElement("canvas");
				canvas.width = Math.max(1, Math.round(w * scale));
				canvas.height = Math.max(1, Math.round(h * scale));
				canvas.getContext("2d").drawImage(img, 0, 0, canvas.width, canvas.height);

				var keepPNG = /^image\/(png|gif)$/.test(file.type);
				var encode = function (type) {
					canvas.toBlob(function (blob) {
						if (!blob) { done(); return; }
						if (blob.size > limit && type === "image/png") { encode("image/jpeg"); return; }
						if (blob.size <= limit) {
							var ext = type === "image/png" ? ".png" : ".jpg";
							var name = (file.name || "picture").replace(/\.[^.]*$/, "") + ext;
							var dt = new DataTransfer();
							dt.items.add(new File([blob], name, { type: type }));
							input.files = dt.files;
						}
						done();
					}, type, 0.85);
				};
				encode(keepPNG ? "image/png" : "image/jpeg");
			};
			img.src = reader.result;
		};
		reader.readAsDataURL(file);
	});

	// Select the invite link on focus so it is easy to copy.
	document.addEventListener("focusin", function (ev) {
		if (ev.target.matches("input[data-select-on-focus]")) { ev.target.select(); }
	});

	// Register the service worker. It caches only /static/, which is what makes
	// the app installable on a phone; see sw.js for why nothing else is cached.
	//
	// The build version rides along in the query. A worker is replaced only when
	// its own bytes change, and sw.js does not change between releases, so
	// without this a new build's assets are invisible to anyone who has already
	// installed one. The version is on <html> so this file stays static.
	if ("serviceWorker" in navigator) {
		window.addEventListener("load", function () {
			var v = document.documentElement.getAttribute("data-asset-version") || "";
			var url = v ? "/sw.js?v=" + encodeURIComponent(v) : "/sw.js";
			navigator.serviceWorker.register(url).catch(function (err) {
				console.warn("service worker registration failed:", err);
			});
		});
	}
})();
