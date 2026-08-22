package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"wishbone/internal/model"
	"wishbone/internal/view"
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
	// One aggregate for every visible list, rather than a fetch per list: it
	// carries the item count as well as the claimed count, so it replaces the
	// per-list item read this loop used to do. It is also the chokepoint — a list
	// of the viewer's own in here is ErrOwnerBlind, not a silent zero.
	otherIDs := make([]string, 0, len(others))
	for _, l := range others {
		otherIDs = append(otherIDs, l.ID)
	}
	progress, err := s.st.ProgressForLists(ctx, otherIDs, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, l := range others {
		p := progress[l.ID]
		data.Others = append(data.Others, templates.VisibleListSummary{
			ListSummary: templates.ListSummary{
				List: l, OwnerName: ownerName[l.OwnerID], ItemCount: p.Items,
			},
			Progress: view.ProgressFrom(p),
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
	page, err := s.vb.BuildListPage(ctx, l, u, model.ParseItemSort(r.URL.Query().Get("sort")))
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
		mine, err := s.st.ListsOwnedBy(ctx, u.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		data.AllUsers = users
		data.SharedIDs = shared
		data.MoveTargets = moveTargets(mine, l.ID)
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

// moveTargets is the owner's other lists: everywhere an item on this list could
// go. A visibility filter would be wrong here — moving something onto a private
// list is a normal thing to want.
func moveTargets(mine []*model.List, currentID string) []templates.MoveTarget {
	var out []templates.MoveTarget
	for _, l := range mine {
		if l.ID == currentID {
			continue
		}
		out = append(out, templates.MoveTarget{ID: l.ID, Name: l.Name})
	}
	return out
}

// listPath is where an action on a list page returns to, keeping a non-default
// sort so the page comes back in the order the person was reading it in.
func listPath(listID string, sort model.ItemSort) string {
	if sort == model.SortManual {
		return "/lists/" + listID
	}
	return "/lists/" + listID + "?sort=" + url.QueryEscape(string(sort))
}

func validVisibility(v string) bool {
	switch v {
	case model.VisibilityPrivate, model.VisibilityAllUsers, model.VisibilitySelected:
		return true
	}
	return false
}
