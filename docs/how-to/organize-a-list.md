# Sort a list and move items between lists

Two ways to tidy up: change the order you read a list in, and move an item to a
different list of your own.

## Sort a list

Open any list you can see — your own or one shared with you — and use the
**Sort** control above the items:

| Order | What you get |
|---|---|
| List order | The owner's own order, arranged with the arrows. The default |
| Price: low to high | Cheapest first. Items with no price go last |
| Price: high to low | Dearest first. Items with no price still go last |
| Recently added | Newest first |
| Added longest ago | Oldest first, which is where the stale things are |
| Category | Grouped by category, in the category order, keeping the owner's order inside each group |

The order lives in the URL (`/lists/<id>?sort=price-asc`), so the back button
works and you can send someone the list already sorted. It is not remembered
between visits: open a list again and you get the owner's own order.

An item with no price sorts last in **both** price directions. An empty price
field means the owner has not said what it costs, which is not the same as it
being cheap.

While a sort is applied, the up and down arrows on your own items are not shown.
They move an item within the stored list order, which is not the order on
screen; switch back to **List order** to use them.

Sorting is available on your own lists too, because nothing it sorts by is
claim-derived — a price, a date you added something, a category you picked. See
[owner-blindness](../explanation/owner-blindness.md).

## Move an item to another list

On your own list, each item carries a **Move to** picker listing your other
lists. Pick one and click **Move**.

The control only appears once you have somewhere to move things to, and only
ever lists lists **you** own. You cannot push an item onto somebody else's list.

The item takes everything with it: its picture, its link, its notes, its
category, and any claims people have already made on it. It lands at the end of
the destination list. You stay on the list you were tidying, so you can move
several things in a row.

**Watch the destination's visibility.** Moving an item to a list only you can see
takes it away from everyone else, including anyone who had already claimed it —
they keep the claim, and it stays on their **Claimed** page, but they can no
longer reach the item on a list page. Wishbone says so in the confirmation when
the destination is private.

People who claimed the item are told it changed, the same way they are told
about an edit: the item is marked on their **Claimed** page and counted in the
badge beside it. You are told nothing about who they are, or that there were any
— the confirmation is the same sentence either way.

## Reorder within one list

Use the **↑** and **↓** buttons on each item, in **List order**. That order is
what everyone else sees when they open the list.
