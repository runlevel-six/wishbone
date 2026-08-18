# Choose the color Wishbone wears

Everyone picks their own. It is a preference on your account, not a setting for
the household — your choice changes nothing for anyone else, including on lists
you share with them.

## Pick one

1. Open **Account**.
2. Under **Color**, choose a palette.
3. Click **Save color**.

The page comes back in it, and stays that way on every device you sign in on.

| Palette | |
|---|---|
| **Forest green** | The original, and the default. Most classic |
| **Cranberry** | Most gift-oriented |
| **Charcoal navy** | Most trustworthy |

## What changes, and what does not

The accent, the links, the buttons, the bow beside the name, and — if your
device is set to dark mode — the near-black background, which leans into the
palette's own hue.

What stays put:

- **Green means saved, red means wrong, gold means look at this.** Those colors
  carry meaning rather than taste, so no palette moves them.
- **The app icon and the favicon.** A browser asks for those long before it
  knows who is looking, so they cannot follow a person's preference. The
  installed app stays green on your home screen whichever palette you pick.
- **The sign-in page**, for the same reason: it exists to find out whose account
  this is.

Dark mode is not one of the choices. Wishbone follows whatever your device is
set to, and every palette is drawn for both.

## Add another palette

The concept work drew six; three shipped. Adding one is two edits, and the test
suite checks they stay in step:

1. `internal/model/model.go` — a `Theme` constant and an entry in `Themes`.
2. `internal/web/static/app.css` — a `--swatch-<name>` constant, a
   `[data-theme="<name>"]` block, and a second block for it inside the
   dark-mode media query.

`TestEveryThemeIsComplete` fails if a palette sets a variable in light mode
without setting it in dark, or if it covers less ground than the others — both
of which look fine on the machine of whoever added it and wrong on somebody
else's. `themeColor` in `internal/web/templates/helpers.go` also needs the hex,
because a `<meta>` tag cannot read a CSS variable; a test compares it against the
stylesheet's own constant.
