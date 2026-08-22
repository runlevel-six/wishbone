// Package view builds the structs templates render.
//
// This package is the chokepoint required by plan §3.2. An item is rendered
// either as an OwnerItemView or as a ViewerItemView, and OwnerItemView has no
// claim fields at all — not hidden fields, absent ones. A template rendering
// the owner's own list therefore cannot reference claim state even by mistake,
// because the field does not exist on the type it was handed.
package view

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"wishbone/internal/categories"
	"wishbone/internal/model"
	"wishbone/internal/store"
)

// Builder constructs view models. It is the only thing outside package store
// that is allowed to ask for claim data.
type Builder struct {
	st *store.Store
}

func New(st *store.Store) *Builder { return &Builder{st: st} }

// Attr is one rendered category attribute.
type Attr struct {
	Key   string
	Label string
	Value string
}

// ItemBase is everything both audiences may see.
type ItemBase struct {
	ID            string
	ListID        string
	Title         string
	URL           string
	Host          string
	Description   string
	Notes         string
	Price         string
	Quantity      int
	CategoryLabel string
	CategorySlug  string
	Attributes    []Attr
	LinkStatus    string
	ImageSHA      string
	SortOrder     int
	EditedAt      string
	// CreatedAt is when the owner added the item (plan §14). It belongs on the
	// base rather than one audience's type because it is the same fact for
	// everyone: it is owner-authored, it does not move when anything is claimed,
	// and it is the one thing on a card that tells a buyer whether to check
	// before buying. A list accumulates for years, and "added last week" and
	// "added in 2021" are different purchases.
	CreatedAt string
}

// OwnerItemView is what a list owner sees. It has no claim fields. Do not add
// any: the type's shape is the enforcement mechanism.
type OwnerItemView struct {
	ItemBase
}

// ClaimView is one claim as shown to a non-owner.
type ClaimView struct {
	ID          string
	ClaimerID   string
	ClaimerName string
	Qty         int
	State       string
	Note        string
	IsMine      bool
}

// ViewerItemView is what everyone except the owner sees.
type ViewerItemView struct {
	ItemBase
	ClaimedQty int
	Available  int
	Claims     []ClaimView
	MyQty      int
	MyClaims   []ClaimView
	// Removed marks an item the owner soft-deleted that this viewer had
	// claimed (plan §3.4).
	Removed bool
	// Notice is a transient UI message shown inside the card after an htmx
	// action, e.g. losing a claim race.
	Notice string
}

// ListPage is the whole list view. Exactly one of OwnerItems / ViewerItems is
// populated, decided by ownership, never by a template.
type ListPage struct {
	List        *model.List
	OwnerName   string
	IsOwner     bool
	OwnerItems  []OwnerItemView
	ViewerItems []ViewerItemView
	SharedWith  []*model.User
	// Sort is the order the items are in, so the page can show which one is
	// active and an action can return to the same view.
	Sort model.ItemSort
}

// Progress is the claimed-vs-total summary of a list, as a non-owning viewer is
// allowed to see it. It answers the one question a buyer opens a list with —
// how much of this is still mine to buy — and it is the reason plan §3.2 lists
// "any 'N remaining' / 'N of M claimed' counter" as a leak vector: the same
// number shown to the owner would tell them how many gifts are on the way.
type Progress struct {
	Items     int
	Claimed   int
	Available int
	Percent   int
}

// ProgressFrom converts the store's aggregate into the render-ready form, and
// returns nil for a list with nothing on it — a nil Progress draws nothing,
// which is what an empty list should look like rather than a 0% bar.
func ProgressFrom(p *store.ListProgress) *Progress {
	if p == nil || p.Items == 0 {
		return nil
	}
	return &Progress{
		Items:     p.Items,
		Claimed:   p.Claimed,
		Available: p.Available(),
		Percent:   p.Percent(),
	}
}

// Progress summarizes how much of this list is already taken care of.
//
// It is computed from ViewerItems, which costs no extra query and, more to the
// point, is empty on an owner's page — so an owner's page physically cannot
// produce a summary, for the same reason an owner's card cannot draw a bar. The
// IsOwner check below is belt to that braces.
func (p *ListPage) Progress() *Progress {
	if p.IsOwner || len(p.ViewerItems) == 0 {
		return nil
	}
	out := &Progress{}
	for _, it := range p.ViewerItems {
		// A removed item is still shown to the viewer who had claimed it
		// (plan §3.4), but it is no longer on the list. Counting it here would
		// leave the list page and the dashboard card — which counts live items
		// only — disagreeing about the same list.
		if it.Removed {
			continue
		}
		out.Items++
		if it.ClaimedQty >= it.Quantity {
			out.Claimed++
		}
	}
	if out.Items == 0 {
		return nil
	}
	out.Available = out.Items - out.Claimed
	out.Percent = out.Claimed * 100 / out.Items
	return out
}

// BuildListPage assembles the list view for one viewer, in the order they asked
// for.
func (b *Builder) BuildListPage(ctx context.Context, list *model.List, viewer *model.User,
	sort model.ItemSort) (*ListPage, error) {

	owner, err := b.st.UserByID(ctx, list.OwnerID)
	if err != nil {
		return nil, err
	}
	page := &ListPage{
		List:      list,
		OwnerName: owner.DisplayName,
		IsOwner:   list.OwnerID == viewer.ID,
		Sort:      sort,
	}

	items, err := b.st.LiveItemsForListSorted(ctx, list.ID, sort)
	if err != nil {
		return nil, err
	}
	images, err := b.st.PrimaryImages(ctx, list.ID)
	if err != nil {
		return nil, err
	}
	cats, err := b.categoryIndex(ctx)
	if err != nil {
		return nil, err
	}

	if page.IsOwner {
		// No claim lookup happens on this branch at all. There is nothing to
		// filter out later because nothing was ever fetched.
		for _, it := range items {
			page.OwnerItems = append(page.OwnerItems, OwnerItemView{ItemBase: b.base(it, cats, images)})
		}
		if list.Visibility == model.VisibilitySelected {
			page.SharedWith, err = b.sharedUsers(ctx, list.ID)
			if err != nil {
				return nil, err
			}
		}
		return page, nil
	}

	claims, err := b.st.ClaimsForList(ctx, list.ID, viewer.ID)
	if err != nil {
		// Belt and braces: if the store says the viewer is the owner, trust
		// the store over the branch above and render nothing.
		if errors.Is(err, model.ErrOwnerBlind) {
			return nil, model.ErrOwnerBlind
		}
		return nil, err
	}
	for _, it := range items {
		page.ViewerItems = append(page.ViewerItems, b.viewerItem(it, claims[it.ID], viewer.ID, cats, images))
	}

	// Items the owner removed sit after the live ones whatever the sort: they are
	// a footnote about something that is gone, not part of what is on offer.
	removed, err := b.st.RemovedClaimedItems(ctx, list.ID, viewer.ID)
	if err != nil {
		return nil, err
	}
	for _, it := range removed {
		v := b.viewerItem(it, claims[it.ID], viewer.ID, cats, images)
		v.Removed = true
		page.ViewerItems = append(page.ViewerItems, v)
	}
	return page, nil
}

// BuildViewerItem renders a single card for a non-owner. It returns
// ErrOwnerBlind if called with the owner, which is what makes an htmx partial
// route as safe as the full page.
func (b *Builder) BuildViewerItem(ctx context.Context, it *model.Item, viewerID string) (ViewerItemView, error) {
	claims, err := b.st.ClaimsForItem(ctx, it.ID, viewerID)
	if err != nil {
		return ViewerItemView{}, err
	}
	cats, err := b.categoryIndex(ctx)
	if err != nil {
		return ViewerItemView{}, err
	}
	images, err := b.imagesForItem(ctx, it.ID)
	if err != nil {
		return ViewerItemView{}, err
	}
	v := b.viewerItem(it, claims, viewerID, cats, images)
	v.Removed = it.DeletedAt != nil
	return v, nil
}

func (b *Builder) viewerItem(it *model.Item, ic *store.ItemClaims, viewerID string,
	cats map[string]*model.Category, images map[string]*model.ItemImage) ViewerItemView {

	v := ViewerItemView{ItemBase: b.base(it, cats, images)}
	if ic == nil {
		v.Available = it.Quantity
		return v
	}
	v.ClaimedQty = ic.ClaimedQty
	v.Available = it.Quantity - ic.ClaimedQty
	if v.Available < 0 {
		v.Available = 0
	}
	for _, c := range ic.Claims {
		cv := ClaimView{
			ID:          c.ID,
			ClaimerID:   c.ClaimerID,
			ClaimerName: c.ClaimerName,
			Qty:         c.Qty,
			State:       c.State,
			Note:        model.Deref(c.Note),
			IsMine:      c.ClaimerID == viewerID,
		}
		v.Claims = append(v.Claims, cv)
		if cv.IsMine {
			v.MyClaims = append(v.MyClaims, cv)
			v.MyQty += cv.Qty
		}
	}
	return v
}

func (b *Builder) base(it *model.Item, cats map[string]*model.Category,
	images map[string]*model.ItemImage) ItemBase {

	base := ItemBase{
		ID:          it.ID,
		ListID:      it.ListID,
		Title:       it.Title,
		URL:         model.Deref(it.URL),
		Description: model.Deref(it.Description),
		Notes:       model.Deref(it.Notes),
		Quantity:    it.Quantity,
		LinkStatus:  it.LinkStatus,
		SortOrder:   it.SortOrder,
		EditedAt:    model.Deref(it.EditedAt),
		CreatedAt:   it.CreatedAt,
	}
	base.Host = hostOf(base.URL)
	base.Price = FormatPrice(it.PriceCents, model.Deref(it.Currency))

	if it.CategoryID != nil {
		if c := cats[*it.CategoryID]; c != nil {
			base.CategoryLabel = c.Label
			base.CategorySlug = c.Slug
			schema, err := categories.ParseSchema(c.FieldSchema)
			if err == nil {
				values := categories.Unmarshal(it.Attributes)
				for _, f := range schema {
					if v := values[f.Key]; v != "" {
						base.Attributes = append(base.Attributes, Attr{Key: f.Key, Label: f.Label, Value: v})
					}
				}
			}
		}
	}
	if im := images[it.ID]; im != nil {
		base.ImageSHA = im.SHA256
	}
	return base
}

func (b *Builder) categoryIndex(ctx context.Context) (map[string]*model.Category, error) {
	cats, err := b.st.Categories(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*model.Category, len(cats))
	for _, c := range cats {
		out[c.ID] = c
	}
	return out, nil
}

func (b *Builder) imagesForItem(ctx context.Context, itemID string) (map[string]*model.ItemImage, error) {
	imgs, err := b.st.ImagesForItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := map[string]*model.ItemImage{}
	if len(imgs) > 0 {
		out[itemID] = imgs[0]
	}
	return out, nil
}

func (b *Builder) sharedUsers(ctx context.Context, listID string) ([]*model.User, error) {
	ids, err := b.st.ShareUserIDs(ctx, listID)
	if err != nil {
		return nil, err
	}
	all, err := b.st.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	var out []*model.User
	for _, u := range all {
		if ids[u.ID] {
			out = append(out, u)
		}
	}
	return out, nil
}

// FormatPrice renders integer cents. Money is never a float (plan §2).
func FormatPrice(cents *int64, currency string) string {
	if cents == nil {
		return ""
	}
	neg := ""
	v := *cents
	if v < 0 {
		neg, v = "-", -v
	}
	amount := fmt.Sprintf("%s%d.%02d", neg, v/100, v%100)
	switch strings.ToUpper(currency) {
	case "", "USD":
		return "$" + amount
	case "EUR":
		return "€" + amount
	case "GBP":
		return "£" + amount
	default:
		return amount + " " + strings.ToUpper(currency)
	}
}

func hostOf(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "www.")
}

// FormatPriceBare renders cents as a plain decimal for form inputs.
func FormatPriceBare(cents int64) string {
	neg := ""
	if cents < 0 {
		neg, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", neg, cents/100, cents%100)
}
