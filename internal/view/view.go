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

	"wishd/internal/categories"
	"wishd/internal/model"
	"wishd/internal/store"
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
}

// BuildListPage assembles the list view for one viewer.
func (b *Builder) BuildListPage(ctx context.Context, list *model.List, viewer *model.User) (*ListPage, error) {
	owner, err := b.st.UserByID(ctx, list.OwnerID)
	if err != nil {
		return nil, err
	}
	page := &ListPage{List: list, OwnerName: owner.DisplayName, IsOwner: list.OwnerID == viewer.ID}

	items, err := b.st.LiveItemsForList(ctx, list.ID)
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
