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
	// Found is what a suspect lookup did read. Showing it is not applying it:
	// the fields stay empty until the owner clicks.
	Found *FoundDetails
	// Accepted marks a form filled from a suspect page because the owner asked
	// for it, which reads differently from a clean lookup and says so.
	Accepted bool
	// LinkStatus rides along so an item created from a lookup keeps what the
	// lookup concluded about its link.
	LinkStatus string
	// Blocked marks a retailer that refused the request — bot protection, not
	// a bad link. BlockedStatus is what it answered.
	Blocked       bool
	BlockedStatus int
	// NothingFound marks a page that was read successfully but carried no
	// usable product details — common on marketplaces that serve an
	// interstitial to non-browser clients.
	NothingFound bool
	Sources      map[string]string
	FetchError   string
	Duplicates   []DuplicateWarning
	FetchEnabled bool
	// AutoLookup runs the link lookup as soon as the page loads, for links
	// arriving from a phone's share sheet.
	AutoLookup bool

	Errors map[string]string
}

// FoundDetails carries a held-back extraction through the warning and back to
// the server if the owner accepts it. It is round-tripped through the form
// rather than re-fetched on the way back so that what is applied is exactly
// what was on screen: a second fetch of the same URL can legitimately answer
// differently — a retailer that rate-limits, a page that changed — and a
// button labelled "use these details" that quietly uses others would be worse
// than the guard it is softening.
type FoundDetails struct {
	Title    string
	Descr    string
	Price    string
	Currency string
	ImageURL string
	URL      string
	URLRaw   string
	// LinkStatus is what the guard concluded, carried so an accepted result
	// keeps it rather than being reset to a cheerier one.
	LinkStatus string
	// Attrs and Sources are JSON objects, carried opaquely.
	Attrs   string
	Sources string
	// Canonical is the address the page claimed for itself: same host,
	// normalized, worth a second lookup. Empty when the page named none, or
	// named one there is no point offering.
	Canonical string
}

// ShareListOption is one destination offered for a shared link.
type ShareListOption struct {
	ID   string
	Name string
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
