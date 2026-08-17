package web

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"wishbone/internal/model"
	"wishbone/internal/web/templates"
)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	mine, err := s.st.ListsOwnedBy(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	others, err := s.st.ListsVisibleTo(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	owners, err := s.st.ListUsers(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ownerName := map[string]string{}
	for _, o := range owners {
		ownerName[o.ID] = o.DisplayName
	}

	data := templates.DashboardData{}
	for _, l := range mine {
		items, err := s.st.LiveItemsForList(ctx, l.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		data.Mine = append(data.Mine, templates.ListSummary{
			List: l, OwnerName: u.DisplayName, ItemCount: len(items), IsOwner: true,
		})
	}
	for _, l := range others {
		items, err := s.st.LiveItemsForList(ctx, l.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		data.Others = append(data.Others, templates.ListSummary{
			List: l, OwnerName: ownerName[l.OwnerID], ItemCount: len(items),
		})
	}

	claims, err := s.st.ClaimsByUser(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.ClaimCount = len(claims)

	s.render(w, r, http.StatusOK, templates.Dashboard(s.page(w, r, ""), data))
}

func (s *Server) handleCreateList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	name := strings.TrimSpace(r.PostFormValue("name"))
	visibility := r.PostFormValue("visibility")
	if name == "" || len(name) > 80 {
		s.flash(w, templates.FlashError, "Give the list a name.")
		s.redirect(w, r, "/")
		return
	}
	if !validVisibility(visibility) {
		visibility = model.VisibilityAllUsers
	}
	l := &model.List{OwnerID: u.ID, Name: name, Visibility: visibility}
	if err := s.st.CreateList(r.Context(), l); err != nil {
		s.fail(w, r, err)
		return
	}
	s.redirect(w, r, "/lists/"+l.ID)
}

func (s *Server) handleViewList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	l, err := s.st.VisibleList(ctx, chi.URLParam(r, "listID"), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.vb.BuildListPage(ctx, l, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data := templates.ListPageData{Page: page, CanEdit: page.IsOwner}
	if page.IsOwner {
		users, err := s.st.ListUsers(ctx)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		shared, err := s.st.ShareUserIDs(ctx, l.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		data.AllUsers = users
		data.SharedIDs = shared
	}
	s.render(w, r, http.StatusOK, templates.ListView(s.page(w, r, l.Name), data))
}

func (s *Server) handleUpdateList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	l, err := s.st.ListByID(ctx, chi.URLParam(r, "listID"))
	if err != nil || l.OwnerID != u.ID {
		s.renderNotFound(w, r)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	visibility := r.PostFormValue("visibility")
	if name == "" || len(name) > 80 {
		s.flash(w, templates.FlashError, "Give the list a name.")
		s.redirect(w, r, "/lists/"+l.ID)
		return
	}
	if !validVisibility(visibility) {
		visibility = l.Visibility
	}
	l.Name, l.Visibility = name, visibility
	if err := s.st.UpdateList(ctx, l); err != nil {
		s.fail(w, r, err)
		return
	}

	shares := r.PostForm["share"]
	// Never share a list with its owner.
	var clean []string
	for _, id := range shares {
		if id != "" && id != l.OwnerID {
			clean = append(clean, id)
		}
	}
	if err := s.st.ReplaceShares(ctx, l.ID, clean); err != nil {
		s.fail(w, r, err)
		return
	}
	s.flash(w, templates.FlashOK, "List updated.")
	s.redirect(w, r, "/lists/"+l.ID)
}

func (s *Server) handleDeleteList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	l, err := s.st.ListByID(ctx, chi.URLParam(r, "listID"))
	if err != nil || l.OwnerID != u.ID {
		s.renderNotFound(w, r)
		return
	}
	shas, err := s.st.ImageSHAsForList(ctx, l.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.st.DeleteList(ctx, l.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.pruneBlobs(ctx, shas)
	s.flash(w, templates.FlashOK, "List deleted.")
	s.redirect(w, r, "/")
}

func validVisibility(v string) bool {
	switch v {
	case model.VisibilityPrivate, model.VisibilityAllUsers, model.VisibilitySelected:
		return true
	}
	return false
}
