# Add items from your phone

Wishbone installs to a phone's home screen and can receive links from the share
sheet, so adding something you're looking at in a shop's app or website takes a
few taps rather than copying a URL between apps.

The install works the same everywhere. Sharing does not: **Android supports web
share targets and iOS does not**, so iPhones need a one-time Shortcut. Both end
up at the same place.

## Install it (both platforms)

**Android, Chrome:** open Wishbone, then the ⋮ menu → *Add to Home screen* (or
accept the install prompt). It opens without browser chrome afterwards.

**iPhone, Safari:** open Wishbone, tap the Share button, then *Add to Home
Screen*. It must be Safari — Chrome on iOS cannot install web apps.

Sign in once after installing. Sessions last 30 days and renew as you use it,
so in practice you sign in a couple of times a year.

## Share a link to Wishbone

### Android

Already works once installed. In any app, share a product page and pick
**Wishbone** from the share sheet. It opens on the add-item form with the link
filled in and the lookup already running.

If Wishbone doesn't appear in the sheet, open the installed app once and try
again — Android registers share targets on first launch.

### iPhone

iOS Safari has no web share target, so this needs a Shortcut. Once, per phone,
about two minutes:

1. Open the **Shortcuts** app → **+** → rename it *Add to Wishbone*.
2. Tap the ⓘ (info) at the bottom, turn on **Show in Share Sheet**, and under
   *Share Sheet Types* leave only **URLs** and **Safari web pages**.
3. Add the action **Open URLs**.
4. Set the URL to your Wishbone address followed by `/share-target?url=`, then
   insert the **Shortcut Input** variable at the end. It should read:

   ```
   https://YOUR-WISHBONE-HOST/share-target?url=[Shortcut Input]
   ```

5. Done. Now sharing any page from Safari (or most apps) offers *Add to
   Wishbone*, which opens the add-item form with the lookup running.

The Shortcut needs building once on each iPhone. You can send a finished
Shortcut to family members through Messages or iCloud sharing, which is much
easier than talking someone through the steps — build it once and share the
file.

## What happens to the link

Whichever route it arrives by, `/share-target` does the same thing:

- **One list** — straight to its add-item form.
- **Several lists** — asks which, then the same form.
- **No lists yet** — says so and sends you to make one, rather than silently
  dropping the link.
- **Signed out** — sign-in first, then on to the form. The link survives.

The form arrives with the link filled in and the lookup already running, so
usually you check the title, maybe fix the price, and save. If the shop
publishes nothing readable, type what you know — a hand-entered item is not a
second-class item.

## What is deliberately not cached

The installed app registers a service worker, which is what makes it
installable and makes the shell load quickly. It caches **only** the
stylesheet, script and icons.

No wishlist page, item picture or claim is ever cached. On a shared family
phone, a cached list could show someone a list they were never shared, or show
a claim that has since been released. Everything except static assets goes to
the network every time.

The practical consequence: Wishbone does not work offline. That is intentional
— an offline wishlist would either be stale or a privacy problem, and usually
both.
