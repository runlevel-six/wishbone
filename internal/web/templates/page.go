// Package templates holds the templ components and the small structs they
// render. User-facing copy says "Wishbone"; everything internal says wishd
// (plan §1).
package templates

import (
	"wishd/internal/model"
	"wishd/internal/view"
)

// ProductName is the name the family says out loud.
const ProductName = "Wishbone"

// FlashKind values.
const (
	FlashInfo  = "info"
	FlashOK    = "ok"
	FlashWarn  = "warn"
	FlashError = "error"
)

type Flash struct {
	Kind string
	Text string
}

// Page is the per-request chrome every full page render needs.
type Page struct {
	Title   string
	User    *model.User
	CSRF    string
	Flashes []Flash
	Path    string
}

func (p Page) IsAdmin() bool { return p.User != nil && p.User.IsAdmin }

func (p Page) DisplayName() string {
	if p.User == nil {
		return ""
	}
	return p.User.DisplayName
}

// ListSummary is one row on the dashboard.
type ListSummary struct {
	List      *model.List
	OwnerName string
	ItemCount int
	IsOwner   bool
}

// Dashboard is the home page model.
type DashboardData struct {
	Mine       []ListSummary
	Others     []ListSummary
	ClaimCount int
}

// ListPageData wraps the view-layer list page with the extras the templates
// need. The claim-bearing and claim-free item slices come straight from
// internal/view and are never merged here.
type ListPageData struct {
	Page        *view.ListPage
	AllUsers    []*model.User
	SharedIDs   map[string]bool
	Categories  []CategoryOption
	CanEdit     bool
	DuplicateOf []DuplicateWarning
}

type CategoryOption struct {
	ID     string
	Slug   string
	Label  string
	Fields []FieldOption
}

type FieldOption struct {
	Key      string
	Label    string
	Type     string
	Required bool
	Options  []string
	Value    string
}

type DuplicateWarning struct {
	ItemTitle string
	ListName  string
}

// ItemFormData drives both the add and edit forms.
type ItemFormData struct {
	ListID     string
	ItemID     string
	Title      string
	URLRaw     string
	URL        string
	Notes      string
	Descr      string
	Price      string
	Currency   string
	Quantity   int
	CategoryID string
	Categories []CategoryOption
	ImageURL   string
	ImageSHA   string

	// Extraction feedback (plan §5.4): a suspect result is shown, never
	// auto-applied.
	Suspect       bool
	SuspectReason []string
	Extracted     bool
	// NothingFound marks a page that was read successfully but carried no
	// usable product details — common on marketplaces that serve an
	// interstitial to non-browser clients.
	NothingFound bool
	Sources      map[string]string
	FetchError   string
	Duplicates   []DuplicateWarning
	FetchEnabled bool

	Errors map[string]string
}

// ClaimedRow is one entry on the "things you've claimed" page.
type ClaimedRow struct {
	ClaimID   string
	ItemTitle string
	ItemURL   string
	ListName  string
	OwnerName string
	Qty       int
	State     string
	Note      string
	Removed   bool
}

// AdminData is the admin page model.
type AdminData struct {
	Users        []*model.User
	Invites      []InviteRow
	Stats        AdminStats
	InviteLink   string
	ClaimDrift   int
	SecretWarn   bool
	SidecarOn    bool
	FetchEnabled bool
}

type AdminStats struct {
	Users  int
	Lists  int
	Items  int
	Images int
}

type InviteRow struct {
	TokenHash string
	CreatedBy string
	CreatedAt string
	ExpiresAt string
	Used      bool
	UsedBy    string
}
