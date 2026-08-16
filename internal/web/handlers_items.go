package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"wishd/internal/categories"
	"wishd/internal/extract"
	"wishd/internal/imgstore"
	"wishd/internal/model"
	"wishd/internal/store"
	"wishd/internal/view"
	"wishd/internal/web/templates"
)

// imageWorkTimeout bounds the outbound image fetch during a save.
const imageWorkTimeout = 15 * time.Second

func (s *Server) handleNewItemForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	l, err := s.st.ListByID(ctx, chi.URLParam(r, "listID"))
	if err != nil || l.OwnerID != u.ID {
		s.renderNotFound(w, r)
		return
	}
	opts, err := s.categoryOptions(ctx, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f := templates.ItemFormData{
		ListID:       l.ID,
		Quantity:     1,
		Categories:   opts,
		CategoryID:   defaultCategoryID(opts),
		Currency:     "USD",
		FetchEnabled: s.ex.Enabled(),
		Errors:       map[string]string{},
	}

	// Arriving from a phone share sheet: prefill the link and run the lookup
	// without waiting to be asked. The point of sharing is not to then tap a
	// button.
	if shared := strings.TrimSpace(r.URL.Query().Get("url")); shared != "" {
		f.URLRaw = shared
		f.AutoLookup = s.ex.Enabled()
	}
	s.render(w, r, http.StatusOK, templates.ItemForm(s.page(w, r, "Add an item"), f))
}

func (s *Server) handleEditItemForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, l, ok := s.ownedItem(w, r)
	if !ok {
		return
	}

	opts, err := s.categoryOptions(ctx, it)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f := templates.ItemFormData{
		ListID:       l.ID,
		ItemID:       it.ID,
		Title:        it.Title,
		URLRaw:       model.Deref(it.URLRaw),
		URL:          model.Deref(it.URL),
		Notes:        model.Deref(it.Notes),
		Descr:        model.Deref(it.Description),
		Price:        priceInput(it.PriceCents),
		Currency:     model.Deref(it.Currency),
		Quantity:     it.Quantity,
		CategoryID:   model.Deref(it.CategoryID),
		Categories:   opts,
		FetchEnabled: s.ex.Enabled(),
		Sources:      categories.Unmarshal(it.FieldSources),
		Errors:       map[string]string{},
	}
	if f.CategoryID == "" {
		f.CategoryID = defaultCategoryID(opts)
	}
	if imgs, err := s.st.ImagesForItem(ctx, it.ID); err == nil && len(imgs) > 0 {
		f.ImageSHA = imgs[0].SHA256
	}
	s.render(w, r, http.StatusOK, templates.ItemForm(s.page(w, r, "Edit item"), f))
}

// handlePreviewItem runs the extractor chain and re-renders the form.
// A suspect result is shown but never applied (plan §5.4).
func (s *Server) handlePreviewItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	l, err := s.st.ListByID(ctx, chi.URLParam(r, "listID"))
	if err != nil || l.OwnerID != u.ID {
		s.renderNotFound(w, r)
		return
	}

	raw := strings.TrimSpace(r.PostFormValue("url_raw"))
	opts, err := s.categoryOptions(ctx, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f := templates.ItemFormData{
		ListID:       l.ID,
		URLRaw:       raw,
		Quantity:     1,
		Currency:     "USD",
		Categories:   opts,
		CategoryID:   defaultCategoryID(opts),
		FetchEnabled: s.ex.Enabled(),
		Sources:      map[string]string{},
		Errors:       map[string]string{},
	}

	if raw == "" {
		s.render(w, r, http.StatusOK, templates.ItemFormBody(s.page(w, r, ""), f))
		return
	}
	if normalized, err := extract.NormalizeURL(raw); err == nil {
		f.URL = normalized
	}

	preview, err := s.ex.Fetch(ctx, raw)
	if err != nil {
		// Logged as well as shown: a lookup that fails for everyone is an
		// operational problem (egress, DNS, a blocked user agent), and the
		// person adding an item is the wrong place to diagnose it from.
		s.log.Info("link lookup failed",
			slog.String("url", raw),
			slog.Any("err", err))
		f.FetchError = friendlyFetchError(err)
		s.render(w, r, http.StatusOK, templates.ItemFormBody(s.page(w, r, ""), f))
		return
	}

	f.URL = preview.URL
	f.Extracted = true
	f.Sources = preview.Result.Sources

	if preview.Suspect() {
		// Show what was found, fill in nothing.
		f.Suspect = true
		f.SuspectReason = preview.Result.SuspectReason
	} else {
		res := preview.Result
		f.Title = res.Title
		f.Descr = res.Description
		if res.PriceCents != nil {
			f.Price = priceInput(res.PriceCents)
		}
		if res.Currency != "" {
			f.Currency = res.Currency
		}
		if len(res.ImageURLs) > 0 {
			f.ImageURL = res.ImageURLs[0]
		}
		// Attributes are carried, but the category stays whatever the user
		// picks: page metadata does not reliably carry a category signal
		// (plan §2.2).
		f.Categories = applyAttributeValues(opts, res.Attributes)

		// A page can be fetched and parsed perfectly and still carry nothing
		// worth having — an interstitial, or a site with no structured data.
		// Saying "filled in from the page" over an empty form is a small lie
		// that wastes the person's time looking for what changed.
		if res.Title == "" && res.PriceCents == nil {
			f.NothingFound = true
		}
	}

	if dups, err := s.st.DuplicateItems(ctx, f.URL, u.ID, ""); err == nil {
		f.Duplicates = s.duplicateWarnings(ctx, dups)
	}
	s.render(w, r, http.StatusOK, templates.ItemFormBody(s.page(w, r, ""), f))
}

func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	l, err := s.st.ListByID(ctx, chi.URLParam(r, "listID"))
	if err != nil || l.OwnerID != u.ID {
		s.renderNotFound(w, r)
		return
	}

	in, _, ok := s.parseItemForm(w, r, l.ID, "")
	if !ok {
		return
	}

	it := &model.Item{
		ListID:       l.ID,
		Title:        in.Title,
		URL:          in.URL,
		URLRaw:       in.URLRaw,
		Description:  in.Description,
		Notes:        in.Notes,
		PriceCents:   in.PriceCents,
		Currency:     in.Currency,
		Quantity:     in.Quantity,
		CategoryID:   in.CategoryID,
		Attributes:   in.Attributes,
		FieldSources: in.FieldSources,
		LinkStatus:   model.LinkUnknown,
	}
	if in.PriceCents != nil {
		it.PriceSeenAt = model.Ptr(model.TimeString(model.Now()))
	}
	if err := s.st.CreateItem(ctx, it); err != nil {
		s.fail(w, r, err)
		return
	}

	s.attachImage(r, it.ID, in)

	if dups, err := s.st.DuplicateItems(ctx, model.Deref(in.URL), u.ID, it.ID); err == nil && len(dups) > 0 {
		s.flash(w, templates.FlashInfo, "Added. Heads up: that link is already on another list you can see.")
	} else {
		s.flash(w, templates.FlashOK, "Added.")
	}
	s.redirect(w, r, "/lists/"+l.ID)
}

func (s *Server) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, l, ok := s.ownedItem(w, r)
	if !ok {
		return
	}

	in, _, ok := s.parseItemForm(w, r, l.ID, it.ID)
	if !ok {
		return
	}

	upd := store.ItemUpdate{
		Title:        in.Title,
		URL:          in.URL,
		URLRaw:       in.URLRaw,
		Description:  in.Description,
		Notes:        in.Notes,
		PriceCents:   in.PriceCents,
		Currency:     in.Currency,
		CategoryID:   in.CategoryID,
		Attributes:   in.Attributes,
		FieldSources: in.FieldSources,
		Quantity:     in.Quantity,
	}
	if in.PriceCents != nil && (it.PriceCents == nil || *it.PriceCents != *in.PriceCents) {
		upd.PriceSeenAt = model.Ptr(model.TimeString(model.Now()))
	} else {
		upd.PriceSeenAt = it.PriceSeenAt
	}
	upd.LinkStatus = it.LinkStatus

	err := s.st.UpdateItem(ctx, it.ID, upd)
	if errors.Is(err, model.ErrConflict) {
		// Deliberate one-bit leak, worded to avoid revealing the count
		// (plan §3.4).
		s.flash(w, templates.FlashWarn,
			"This item can't be reduced right now — try removing it instead.")
		s.redirect(w, r, "/items/"+it.ID+"/edit")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}

	if r.PostFormValue("remove_image") == "1" {
		s.removeImages(ctx, it.ID)
	}
	s.attachImage(r, it.ID, in)

	s.flash(w, templates.FlashOK, "Saved.")
	s.redirect(w, r, "/lists/"+l.ID)
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, l, ok := s.ownedItem(w, r)
	if !ok {
		return
	}
	// Note the SHAs first: the rows are about to go away, and an orphaned blob
	// is both wasted space and a picture that outlives the item it belonged to.
	shas := s.imageSHAsForItem(ctx, it.ID)

	if err := s.st.DeleteItem(ctx, it.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	// A soft-deleted item keeps its image rows, so the reference count inside
	// pruneBlobs leaves those files alone by itself.
	s.pruneBlobs(ctx, shas)
	// The message is identical whether the row was soft-deleted or removed
	// outright, so it carries no claim information.
	s.flash(w, templates.FlashOK, "Removed from your list.")
	s.redirect(w, r, "/lists/"+l.ID)
}

func (s *Server) handleMoveItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, l, ok := s.ownedItem(w, r)
	if !ok {
		return
	}
	up := r.PostFormValue("dir") != "down"
	if err := s.st.MoveItem(ctx, l.ID, it.ID, up); err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/lists/"+l.ID)
}

// handleCategoryFields serves the dynamic field block when the category
// selector changes.
func (s *Server) handleCategoryFields(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	catID := r.URL.Query().Get("category_id")
	opts, err := s.categoryOptions(ctx, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	chosen := templates.CategoryOption{}
	for _, c := range opts {
		if c.ID == catID {
			chosen = c
			break
		}
	}
	// Preserve anything already typed into matching field names.
	_ = r.ParseForm()
	for i, f := range chosen.Fields {
		if v := r.Form.Get("attr_" + f.Key); v != "" {
			chosen.Fields[i].Value = v
		}
	}
	s.render(w, r, http.StatusOK, templates.CategoryFields(chosen))
}

// itemInput is the validated form payload.
type itemInput struct {
	Title        string
	URL          *string
	URLRaw       *string
	Description  *string
	Notes        *string
	PriceCents   *int64
	Currency     *string
	Quantity     int
	CategoryID   *string
	Attributes   string
	FieldSources string
	ImageURL     string
}

// parseItemForm validates a submitted item, re-rendering the form on failure.
func (s *Server) parseItemForm(w http.ResponseWriter, r *http.Request, listID, itemID string) (itemInput, templates.ItemFormData, bool) {
	ctx := r.Context()
	var in itemInput

	title := strings.TrimSpace(r.PostFormValue("title"))
	rawURL := strings.TrimSpace(r.PostFormValue("url_raw"))
	notes := strings.TrimSpace(r.PostFormValue("notes"))
	descr := strings.TrimSpace(r.PostFormValue("description"))
	priceStr := strings.TrimSpace(r.PostFormValue("price"))
	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency")))
	qtyStr := strings.TrimSpace(r.PostFormValue("quantity"))
	catID := strings.TrimSpace(r.PostFormValue("category_id"))

	errs := map[string]string{}

	if title == "" || len(title) > 200 {
		errs["title"] = "Give the item a name."
	}
	qty := 1
	if qtyStr != "" {
		n, err := strconv.Atoi(qtyStr)
		if err != nil || n < 1 || n > 99 {
			errs["quantity"] = "Pick a number between 1 and 99."
		} else {
			qty = n
		}
	}
	var cents *int64
	if priceStr != "" {
		c, cur := extract.ParsePriceCents(priceStr)
		if c == nil {
			errs["price"] = "That does not look like a price."
		}
		cents = c
		if currency == "" {
			currency = cur
		}
	}
	if currency != "" && len(currency) != 3 {
		currency = ""
	}

	// Attributes are validated against the selected category's schema; unknown
	// keys are rejected rather than silently stored (plan §2.2).
	submitted := map[string]string{}
	_ = r.ParseForm()
	for k, vs := range r.PostForm {
		if strings.HasPrefix(k, "attr_") && len(vs) > 0 {
			submitted[strings.TrimPrefix(k, "attr_")] = vs[0]
		}
	}
	var attrsJSON = "{}"
	var cat *model.Category
	if catID != "" {
		c, err := s.st.CategoryByID(ctx, catID)
		if err != nil {
			errs["category"] = "Unknown category."
		} else {
			cat = c
		}
	}
	if cat != nil {
		schema, err := categories.ParseSchema(cat.FieldSchema)
		if err != nil {
			s.log.Error("category schema", slog.String("slug", cat.Slug), slog.Any("err", err))
		}
		clean, verr := categories.Validate(schema, submitted)
		if verr != nil {
			var ve *categories.ValidationError
			if errors.As(verr, &ve) {
				for k, msg := range ve.Problems {
					errs["attr_"+k] = msg
				}
			} else {
				errs["category"] = verr.Error()
			}
		} else {
			attrsJSON, _ = categories.Marshal(clean)
		}
	}

	if len(errs) > 0 {
		opts, err := s.categoryOptions(ctx, nil)
		if err != nil {
			s.fail(w, r, err)
			return in, templates.ItemFormData{}, false
		}
		f := templates.ItemFormData{
			ListID:       listID,
			ItemID:       itemID,
			Title:        title,
			URLRaw:       rawURL,
			Notes:        notes,
			Descr:        descr,
			Price:        priceStr,
			Currency:     currency,
			Quantity:     qty,
			CategoryID:   catID,
			Categories:   applyAttributeValues(opts, submitted),
			FetchEnabled: s.ex.Enabled(),
			Errors:       errs,
		}
		s.render(w, r, http.StatusBadRequest, templates.ItemForm(s.page(w, r, "Item"), f))
		return in, f, false
	}

	in.Title = title
	in.Quantity = qty
	in.PriceCents = cents
	in.Attributes = attrsJSON
	in.ImageURL = strings.TrimSpace(r.PostFormValue("image_url"))

	if rawURL != "" {
		in.URLRaw = &rawURL
		// Always re-normalize server-side rather than trusting the hidden
		// field the form round-tripped.
		if n, err := extract.NormalizeURL(rawURL); err == nil && n != "" {
			in.URL = &n
		}
	}
	if notes != "" {
		in.Notes = &notes
	}
	if descr != "" {
		in.Description = &descr
	}
	if currency != "" {
		in.Currency = &currency
	}
	if cat != nil {
		in.CategoryID = &cat.ID
	}

	sources := categories.Unmarshal(r.PostFormValue("field_sources"))
	// Anything the person typed is theirs; mark it so a future re-scrape does
	// not overwrite a human correction (plan §5.3).
	if sources == nil {
		sources = map[string]string{}
	}
	if _, ok := sources["title"]; !ok {
		sources["title"] = extract.SourceUser
	}
	fs, _ := categories.Marshal(sources)
	in.FieldSources = fs

	return in, templates.ItemFormData{}, true
}

// ownedItem loads an item and verifies the caller owns its list.
func (s *Server) ownedItem(w http.ResponseWriter, r *http.Request) (*model.Item, *model.List, bool) {
	ctx := r.Context()
	u := userFrom(ctx)
	it, err := s.st.ItemByID(ctx, chi.URLParam(r, "itemID"))
	if err != nil || it.DeletedAt != nil {
		s.renderNotFound(w, r)
		return nil, nil, false
	}
	l, err := s.st.ListByID(ctx, it.ListID)
	if err != nil || l.OwnerID != u.ID {
		s.renderNotFound(w, r)
		return nil, nil, false
	}
	return it, l, true
}

// attachImage stores an uploaded file, or fetches the extractor's image.
// Failures are logged, never fatal: an item without a picture is fine.
func (s *Server) attachImage(r *http.Request, itemID string, in itemInput) {
	ctx := r.Context()
	if file, header, err := r.FormFile("image"); err == nil {
		defer file.Close()
		if header.Size > 0 {
			raw, err := io.ReadAll(io.LimitReader(file, imgstore.MaxImageBytes))
			if err != nil {
				s.log.Warn("read uploaded image", slog.Any("err", err))
			} else if stored, err := s.img.Store(raw); err != nil {
				s.log.Warn("store uploaded image", slog.Any("err", err))
			} else {
				s.saveImageRow(ctx, itemID, stored)
				return
			}
		}
	}
	if in.ImageURL == "" || !s.ex.Enabled() {
		return
	}
	// Detach from the request so a slow retailer does not block the redirect
	// past the handler's own deadline.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageWorkTimeout)
	defer cancel()
	stored, err := s.img.FetchAndStore(fetchCtx, in.ImageURL)
	if err != nil {
		s.log.Info("image fetch failed", slog.String("url", in.ImageURL), slog.Any("err", err))
		return
	}
	s.saveImageRow(fetchCtx, itemID, stored)
}

func (s *Server) saveImageRow(ctx context.Context, itemID string, stored *imgstore.Stored) {
	img := &model.ItemImage{
		ItemID:    itemID,
		SHA256:    stored.SHA256,
		Mime:      stored.Mime,
		Width:     &stored.Width,
		Height:    &stored.Height,
		IsPrimary: true,
	}
	if err := s.st.AddItemImage(ctx, img); err != nil {
		s.log.Warn("record image", slog.Any("err", err))
	}
}

func (s *Server) removeImages(ctx context.Context, itemID string) {
	imgs, err := s.st.ImagesForItem(ctx, itemID)
	if err != nil {
		return
	}
	var shas []string
	for _, im := range imgs {
		if err := s.st.DeleteItemImage(ctx, im.ID, itemID); err != nil {
			continue
		}
		shas = append(shas, im.SHA256)
	}
	s.pruneBlobs(ctx, shas)
}

func (s *Server) imageSHAsForItem(ctx context.Context, itemID string) []string {
	imgs, err := s.st.ImagesForItem(ctx, itemID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(imgs))
	for _, im := range imgs {
		out = append(out, im.SHA256)
	}
	return out
}

// pruneBlobs deletes image files no database row points at any more. Blobs are
// content-addressed and shared between items, so the reference count is what
// decides — never the fact that one item let go of it.
func (s *Server) pruneBlobs(ctx context.Context, shas []string) {
	for _, sha := range shas {
		n, err := s.st.ImageRefCount(ctx, sha)
		if err != nil || n > 0 {
			continue
		}
		if err := s.img.Remove(sha); err != nil {
			s.log.Warn("remove image blob", slog.Any("err", err))
		}
	}
}

func (s *Server) categoryOptions(ctx context.Context, it *model.Item) ([]templates.CategoryOption, error) {
	cats, err := s.st.Categories(ctx)
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if it != nil {
		values = categories.Unmarshal(it.Attributes)
	}
	var out []templates.CategoryOption
	for _, c := range cats {
		schema, err := categories.ParseSchema(c.FieldSchema)
		if err != nil {
			return nil, err
		}
		opt := templates.CategoryOption{ID: c.ID, Slug: c.Slug, Label: c.Label}
		for _, f := range schema {
			opt.Fields = append(opt.Fields, templates.FieldOption{
				Key: f.Key, Label: f.Label, Type: f.Type, Required: f.Required,
				Options: f.Options, Value: values[f.Key],
			})
		}
		out = append(out, opt)
	}
	return out, nil
}

func (s *Server) duplicateWarnings(ctx context.Context, items []*model.Item) []templates.DuplicateWarning {
	var out []templates.DuplicateWarning
	for _, it := range items {
		l, err := s.st.ListByID(ctx, it.ListID)
		if err != nil {
			continue
		}
		out = append(out, templates.DuplicateWarning{ItemTitle: it.Title, ListName: l.Name})
	}
	return out
}

// applyAttributeValues pre-fills category fields from extracted attributes.
func applyAttributeValues(opts []templates.CategoryOption, values map[string]string) []templates.CategoryOption {
	if len(values) == 0 {
		return opts
	}
	for i := range opts {
		for j := range opts[i].Fields {
			if v := values[opts[i].Fields[j].Key]; v != "" {
				opts[i].Fields[j].Value = v
			}
		}
	}
	return opts
}

func defaultCategoryID(opts []templates.CategoryOption) string {
	for _, c := range opts {
		if c.Slug == "general" {
			return c.ID
		}
	}
	if len(opts) > 0 {
		return opts[0].ID
	}
	return ""
}

func priceInput(cents *int64) string {
	if cents == nil {
		return ""
	}
	return view.FormatPriceBare(*cents)
}

func friendlyFetchError(err error) string {
	switch {
	case errors.Is(err, extract.ErrDisabled):
		return "link lookup is switched off"
	default:
		msg := err.Error()
		if i := strings.LastIndex(msg, ": "); i > 0 && len(msg) > 80 {
			msg = msg[i+2:]
		}
		return msg
	}
}
