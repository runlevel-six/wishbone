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

	// Select the invite link on focus so it is easy to copy.
	document.addEventListener("focusin", function (ev) {
		if (ev.target.matches("input[data-select-on-focus]")) { ev.target.select(); }
	});

	// Register the service worker. It caches only /static/, which is what makes
	// the app installable on a phone; see sw.js for why nothing else is cached.
	if ("serviceWorker" in navigator) {
		window.addEventListener("load", function () {
			navigator.serviceWorker.register("/sw.js").catch(function (err) {
				console.warn("service worker registration failed:", err);
			});
		});
	}
})();
