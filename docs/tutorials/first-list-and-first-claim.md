# Your first list and first claim

By the end of this tutorial you will have Wishbone running on your own machine,
two accounts, a list with something on it, a claim against that item — and you
will have seen, with your own eyes, that the list owner cannot tell.

It takes about ten minutes. Everything happens locally; nothing is deployed and
nothing is sent anywhere.

## What you need

- Go 1.26 or newer
- A terminal and a browser

## 1. Get the app running

From the repository root:

```sh
make tools
WISHD_DEV_PASSWORD=localdevpassword make run
```

The first line installs the template compiler. The second starts the app on
<http://127.0.0.1:8080> with its data in `./tmp`, and — because the database is
empty — creates an administrator account for you.

Look for this line in the output:

```
{"level":"INFO","msg":"created bootstrap admin","username":"..."}
```

The username is your operating-system username; the password is the one you
just set on the command line.

Leave this running and open a second terminal for later steps.

## 2. Sign in

Visit <http://127.0.0.1:8080>. You will be sent to the sign-in page. Use the
username from the log line and `localdevpassword`.

You are now looking at an empty **Lists** page.

## 3. Make a list

Click **New list**. Call it `Birthday`, leave the visibility as *Everyone in
the family*, and create it.

Wishbone drops you on the list, which is empty.

## 4. Add something to it

Click **Add item**. Ignore the "Start from a link" box for now — you will come
back to it — and fill in the form below it:

- **What is it?** `Cast iron skillet`
- **Price** `39.99`
- **How many?** `2`
- **Notes for whoever buys it** `12 inch, pre-seasoned`

Click **Add to list**.

The item appears with its price and note. Notice what is *not* on the card:
there is nothing about claims, because you own this list.

## 5. Invite a second person

You need someone to claim the skillet. Click **Admin** in the header, then
**Create invite link**.

A link appears at the top of the page:

```
http://127.0.0.1:8080/register/xxxxxxxxxxxxxxxxxxxx
```

Copy it. Wishbone never emails anything — invite links are meant to be pasted
into whatever chat your family already uses.

## 6. Register as that second person

Open a **private/incognito window** — you need a separate session, not a second
tab — and paste the invite link.

Fill in the registration form:

- **Name your family will recognize** `Sam`
- **Username** `sam`
- **Password** something at least ten characters long

Submit it. Sam is now signed in, in that window, and can see your `Birthday`
list under **Family lists**.

## 7. Claim the skillet

As Sam, open the `Birthday` list. The skillet card looks different from here:
it says **2 of 2 still needed** and offers a quantity box and an **I'll get
this** button.

Set the quantity to `1` and click **I'll get this**.

The card updates in place:

- it now says **1 of 2 still needed**
- a line appears reading **You ×1 claimed**, with **Mark bought** and
  **Release** buttons
- underneath, a note: *The owner of this list cannot see any of this.*

Click **Mark bought**. The tag changes to *bought*.

## 8. Check that from the other side

Switch back to your first window — the one signed in as the list owner — and
reload the `Birthday` list.

The skillet card is **exactly as you left it**. No claim, no counter, no "1 of
2 remaining", no hint that anything happened. The page is byte-for-byte
identical to what it was before Sam claimed anything, which is a property the
test suite enforces on every release.

This is not the interface hiding something it knows. When Wishbone builds a
page for a list's owner, the claim data is never fetched, and the type used to
render the item has no field it could live in. See
[Owner-blindness](../explanation/owner-blindness.md) for how that is arranged.

## 9. Try the link lookup

Back as the owner, click **Add item**, and paste a real product URL into
**Start from a link**, then click **Look it up**.

One of three things happens, and all three are fine:

- The form fills in with a title, price and picture. Change anything that looks
  wrong and save.
- Wishbone tells you the link looks wrong and fills in *nothing*. That is the
  soft-404 guard: some retailers answer a dead product link with a perfectly
  valid-looking page describing something else, and a confidently wrong item is
  worse than an empty form.
- It says it could not read the page. Type the details in yourself; a
  hand-entered item is not a second-class item here.

## Where to go next

- [Run Wishbone locally](../how-to/run-locally.md) for the day-to-day
  development loop
- [Deploy to Kubernetes](../how-to/deploy.md) when you want the family on it
- [Owner-blindness](../explanation/owner-blindness.md) for the reasoning behind
  step 8

## Clean up

Stop the server with `Ctrl-C` and delete the local data:

```sh
make clean
```
