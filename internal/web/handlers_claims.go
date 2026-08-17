package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"wishd/internal/model"
	"wishd/internal/store"
	"wishd/internal/web/templates"
)

// claimableItem loads an item the viewer may claim from: visible, live, and
// not their own. Anything else is a 404 rather than an explanation.
func (s *Server) claimableItem(w http.ResponseWriter, r *http.Request) (*model.Item, bool) {
	ctx := r.Context()
	u := userFrom(ctx)
	it, err := s.st.ItemByID(ctx, chi.URLParam(r, "itemID"))
	if err != nil || it.DeletedAt != nil {
		s.renderNotFound(w, r)
		return nil, false
	}
	l, err := s.st.VisibleList(ctx, it.ListID, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return nil, false
	}
	if l.OwnerID == u.ID {
		// The owner cannot claim from their own list (plan §3.3).
		s.renderNotFound(w, r)
		return nil, false
	}
	return it, true
}

func (s *Server) handleCreateClaim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	it, ok := s.claimableItem(w, r)
	if !ok {
		return
	}

	qty := 1
	if v := strings.TrimSpace(r.PostFormValue("qty")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			qty = 1
		} else {
			qty = n
		}
	}

	_, err := s.st.CreateClaim(ctx, it.ID, u.ID, qty, nil)
	switch {
	case errors.Is(err, model.ErrConflict):
		s.respondItemCard(w, r, it.ID, "Someone just claimed this — here is where it stands now.")
		return
	case errors.Is(err, model.ErrNotFound):
		s.renderNotFound(w, r)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	s.respondItemCard(w, r, it.ID, "")
}

func (s *Server) handleReleaseClaim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	itemID, err := s.st.ReleaseClaim(ctx, chi.URLParam(r, "claimID"), u.ID)
	if errors.Is(err, model.ErrNotFound) {
		s.renderNotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.respondItemCard(w, r, itemID, "")
}

func (s *Server) handleClaimState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	state := r.PostFormValue("state")
	itemID, err := s.st.SetClaimState(ctx, chi.URLParam(r, "claimID"), u.ID, state)
	switch {
	case errors.Is(err, model.ErrNotFound):
		s.renderNotFound(w, r)
		return
	case errors.Is(err, model.ErrConflict):
		s.back(w, r, "/claims")
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	s.respondItemCard(w, r, itemID, "")
}

func (s *Server) handleClaimNote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	note := strings.TrimSpace(r.PostFormValue("note"))
	var notePtr *string
	if note != "" {
		if len(note) > 500 {
			note = note[:500]
		}
		notePtr = &note
	}
	err := s.st.SetClaimNote(ctx, chi.URLParam(r, "claimID"), u.ID, notePtr)
	if errors.Is(err, model.ErrNotFound) {
		s.renderNotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.flash(w, templates.FlashOK, "Note saved. The list owner cannot see it.")
	s.back(w, r, "/claims")
}

// respondItemCard re-renders one item for a non-owner after a claim change.
// For a non-htmx client it falls back to a redirect.
func (s *Server) respondItemCard(w http.ResponseWriter, r *http.Request, itemID, notice string) {
	ctx := r.Context()
	u := userFrom(ctx)

	it, err := s.st.ItemByID(ctx, itemID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !isHTMX(r) {
		if notice != "" {
			s.flash(w, templates.FlashWarn, notice)
		}
		s.back(w, r, "/lists/"+it.ListID)
		return
	}

	card, err := s.vb.BuildViewerItem(ctx, it, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	card.Notice = notice
	s.render(w, r, http.StatusOK, templates.ViewerItemCard(s.page(w, r, ""), card))
}

func (s *Server) handleMyClaims(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	claimed, err := s.st.ClaimsByUser(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Opening this page is what "seen" means, so the watermark moves here and
	// the badge is gone by the time the chrome is built below. The old value is
	// kept to mark the rows it was pointing at (plan §12).
	watermark, err := s.st.MarkClaimsSeen(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	var rows []templates.ClaimedRow
	for _, c := range claimed {
		rows = append(rows, templates.ClaimedRow{
			ClaimID:   c.Claim.ID,
			ItemTitle: c.Item.Title,
			ItemURL:   model.Deref(c.Item.URL),
			ListName:  c.ListName,
			OwnerName: c.OwnerName,
			Qty:       c.Claim.Qty,
			State:     c.Claim.State,
			Note:      model.Deref(c.Claim.Note),
			Removed:   c.Item.DeletedAt != nil,
			EditedAt:  model.Deref(c.Item.EditedAt),
			Changed:   store.ChangedSince(c.Item, c.Claim, watermark),
		})
	}
	s.render(w, r, http.StatusOK, templates.Claims(s.page(w, r, "Claimed"), rows))
}
