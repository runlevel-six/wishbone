package web

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"wishbone/internal/model"
	"wishbone/internal/web/templates"
)

// The admin reconciliation report (plan §13).
//
// Owner-blindness is right by default and it is also unfalsifiable from the
// inside: somebody who thinks their claim did not register cannot be shown the
// state they are asking about, and neither can the list owner. This is the one
// place that can answer "what does the database actually say", and every rule
// around it exists to keep that from becoming a way to spoil surprises.
//
// The report covers every list, the admin's own included — an admin whose own
// list is misbehaving is exactly the person who reports it. Their own lists take
// two separate deliberate actions: come to the report, then switch on inclusion,
// which is a POST rather than a URL so it cannot be bookmarked, shared or
// re-entered from history into a single click. The switch lasts for the browser
// session and no longer.
//
// For a non-admin every route here is a 404, like the rest of /admin.

// ownReportCookie carries the "include my own lists" switch.
//
// A session cookie, so it dies with the browser rather than living in the
// session row for thirty days: "this visit" is the promise the design makes.
// Scoped to /admin so it is never sent with an ordinary page, and HttpOnly so the
// only thing that can set it is the POST below.
const ownReportCookie = "wishbone_admin_own"

func (s *Server) includeOwnLists(r *http.Request) bool {
	c, err := r.Cookie(ownReportCookie)
	return err == nil && c.Value == "on"
}

// handleAdminOwnToggle is the second of the two actions. Turning it on says what
// is about to happen and is logged; turning it off is a click and is not.
func (s *Server) handleAdminOwnToggle(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	on := r.PostFormValue("include") == "on"

	http.SetCookie(w, &http.Cookie{
		Name:     ownReportCookie,
		Value:    map[bool]string{true: "on", false: ""}[on],
		Path:     "/admin",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   map[bool]int{true: 0, false: -1}[on],
	})

	if on {
		s.log.Info("admin enabled own-list reconciliation",
			slog.String("admin_id", u.ID), slog.String("admin", u.Username))
		s.flash(w, templates.FlashWarn,
			"Your own lists are now included. You will see claims on them, and that cannot be un-seen.")
	} else {
		s.flash(w, templates.FlashInfo, "Your own lists are hidden again.")
	}
	s.back(w, r, "/admin")
}

// handleAdminPersonLists lists one person's lists as an entry point to the
// report. The people table on /admin is the finder; this is the second step.
func (s *Server) handleAdminPersonLists(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := userFrom(ctx)

	person, err := s.st.UserByID(ctx, chi.URLParam(r, "userID"))
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data := templates.AuditPersonData{
		PersonID:    person.ID,
		PersonName:  person.DisplayName,
		IsSelf:      person.ID == admin.ID,
		IncludeOwn:  s.includeOwnLists(r),
		ToggleIsOwn: person.ID == admin.ID,
	}
	// Their own lists, with the switch off: say so and offer the switch rather
	// than pretend the person does not exist. The refusal is the feature.
	if data.IsSelf && !data.IncludeOwn {
		s.render(w, r, http.StatusOK, templates.AuditPerson(s.page(w, r, "Reconciliation"), data))
		return
	}

	lists, err := s.st.ListsOwnedBy(ctx, person.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, l := range lists {
		items, err := s.st.AuditItemsForList(ctx, l.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		data.Lists = append(data.Lists, templates.AuditListRow{
			ID:         l.ID,
			Name:       l.Name,
			Visibility: l.Visibility,
			Items:      len(items),
		})
	}
	s.render(w, r, http.StatusOK, templates.AuditPerson(s.page(w, r, "Reconciliation"), data))
}

// handleAdminListState is the report itself: every item, every claim, who holds
// it, and whether the denormalized count agrees with the rows.
func (s *Server) handleAdminListState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := userFrom(ctx)

	list, err := s.st.ListByID(ctx, chi.URLParam(r, "listID"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A stale link to their own list must not spoil anything, so this is the
	// ordinary does-not-exist-or-not-yours answer rather than an explanation.
	if list.OwnerID == admin.ID && !s.includeOwnLists(r) {
		s.renderNotFound(w, r)
		return
	}
	owner, err := s.st.UserByID(ctx, list.OwnerID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	items, err := s.st.AuditItemsForList(ctx, list.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	claims, err := s.st.AuditClaimsForList(ctx, list.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	data := templates.AuditListData{
		ListID:     list.ID,
		ListName:   list.Name,
		OwnerID:    owner.ID,
		OwnerName:  owner.DisplayName,
		IsOwnList:  list.OwnerID == admin.ID,
		Visibility: list.Visibility,
	}
	for _, it := range items {
		row := templates.AuditItemRow{
			Title:      it.Title,
			Quantity:   it.Quantity,
			ClaimedQty: it.ClaimedQty,
			Removed:    it.DeletedAt != nil,
			Added:      it.CreatedAt,
		}
		if ic := claims[it.ID]; ic != nil {
			row.ClaimSum = ic.ClaimedQty
			for _, c := range ic.Claims {
				row.Claims = append(row.Claims, templates.AuditClaimRow{
					ClaimerName: c.ClaimerName,
					Qty:         c.Qty,
					State:       c.State,
					// Whether a note exists, never its text: it is
					// claimer-to-claimer coordination and the most personal
					// field in the schema, and "does a claim exist and for how
					// many" is the question this report answers.
					HasNote:   model.Deref(c.Note) != "",
					CreatedAt: c.CreatedAt,
					UpdatedAt: c.UpdatedAt,
				})
			}
		}
		// The §2.1 invariant, per item. /admin/health checks it instance-wide;
		// here it is attached to the row somebody is asking about.
		row.Drift = row.ClaimedQty != row.ClaimSum
		if row.Drift {
			data.Drift++
		}
		data.Items = append(data.Items, row)
	}

	s.log.Info("admin viewed list reconciliation",
		slog.String("admin_id", admin.ID),
		slog.String("admin", admin.Username),
		slog.String("list_id", list.ID),
		slog.Bool("own_list", data.IsOwnList))

	s.render(w, r, http.StatusOK, templates.AuditList(s.page(w, r, "Reconciliation"), data))
}
