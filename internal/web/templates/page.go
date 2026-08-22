// Package templates holds the templ components and the small structs they
// render. User-facing copy says "Wishbone"; everything internal says wishbone
// (plan §1).
package templates

import (
	"wishbone/internal/model"
	"wishbone/internal/view"
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
	// ClaimUpdates is how many items this person has claimed that the owner has
	// edited or removed since they last looked (plan §12). Their own claims
	// only; nothing here describes anyone else's.
	ClaimUpdates int
	// Asset is the build version, appended to every /static/ URL so a release
	// retires the previous release's cached files. See asset().
	Asset string
	// Theme is the palette this reader chose, rendered onto <html> so one
	// attribute switches every color in the stylesheet. Anonymous pages carry
	// the default: a theme belongs to an account.
	Theme model.Theme
}

// ThemeOption is one palette in the appearance picker.
type ThemeOption struct {
	Value model.Theme
	Label string
	// Note is the one-line character sketch from the concept sheet. It is what
	// makes the list choosable without a preview of the whole app.
	Note string
	// Swatch is the CSS class that paints this palette's color, so the picker
	// shows the colors rather than describing them.
	Swatch string
}

func (p Page) IsAdmin() bool { return p.User != nil && p.User.IsAdmin }

func (p Page) DisplayName() string {
	if p.User == nil {
		return ""
	}
	return p.User.DisplayName
}

// Username is the name this person signs in with, for the account form to show
// back to them.
func (p Page) Username() string {
	if p.User == nil {
		return ""
	}
	return p.User.Username
}

// ListSummary is one of your own lists on the dashboard.
//
// It has no claim fields and none may be added. This is the OwnerItemView rule
// (plan §3.2) at the size of a whole list: a card built from this type cannot
// draw a claimed-vs-total bar, because there is nothing on it to draw one from.
type ListSummary struct {
	List      *model.List
	OwnerName string
	ItemCount int
	IsOwner   bool
}

// VisibleListSummary is somebody else's list on the dashboard, where how much
// is already claimed is the thing a buyer came to find out.
type VisibleListSummary struct {
	ListSummary
	// Progress is nil for a list with no items, and draws nothing.
	Progress *view.Progress
}

// Dashboard is the home page model. The two slices carry different types on
// purpose, so the claim-bearing one cannot be handed to the owner's loop.
type DashboardData struct {
	Mine       []ListSummary
	Others     []VisibleListSummary
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
	// MoveTargets are the owner's other lists, offered as destinations for an
	// item. Empty for a viewer, and empty for an owner with nowhere else to put
	// anything, in which case the control is left out entirely.
	MoveTargets []MoveTarget
}

// MoveTarget is one list an item can be moved to. Only ever the owner's own
// lists: an item cannot be pushed onto somebody else's.
type MoveTarget struct {
	ID   string
	Name string
}

// OwnerCardOptions is the page context an owner's item card needs beyond the
// item itself.
//
// It is a small purpose-built struct rather than ListPageData for the same
// reason OwnerItemView has no claim fields (plan §3.2): nothing handed to an
// owner-facing card should have claim-bearing data hanging off it, and
// ListPageData reaches the viewer item slice through Page.
type OwnerCardOptions struct {
	MoveTargets []MoveTarget
	// CanReorder is false while a sort is active. The arrows move an item within
	// the stored order, which is not the order on screen, so they are left out
	// rather than made to lie.
	CanReorder bool
	// Sort rides along so the move form comes back to the view it was used from.
	Sort model.ItemSort
}

// SortOption is one entry in the sort control.
type SortOption struct {
	Value model.ItemSort
	Label string
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
	// NotProduct names the shape of an address that was recognized as a listing
	// rather than a product, so nothing was fetched at all. NotProductLabel is
	// the same fact phrased for a person to read.
	NotProduct      string
	NotProductLabel string
	// TitleGuessed marks a title taken from the address rather than the page,
	// because nothing could read the page. It is filled in, and the form says so:
	// a guess presented as a fact is the thing this project does not do.
	TitleGuessed bool
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
// button labeled "use these details" that quietly uses others would be worse
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

// HelpData is everything the help page is allowed to know.
//
// Two facts about this instance, and no data belonging to anybody. The page is
// prose; these are here because prose that describes a box which is not on the
// screen, or tells somebody to type a host they would have to guess, is worse
// than no help at all.
type HelpData struct {
	// ShareTargetURL is this instance's own share-target address, spelled out so
	// the iPhone shortcut can be built by copying rather than by guessing.
	ShareTargetURL string
	// FetchEnabled says whether link lookup exists here. With it off, the whole
	// "start from a link" story describes a box that is not there.
	FetchEnabled bool
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
	// EditedAt is the owner's last edit, and Changed marks the rows the unread
	// badge was counting: touched since this person last opened the page.
	EditedAt string
	Changed  bool
}

// The admin reconciliation report (plan §13). Deliberately flat data: the
// report is a table somebody reads once to answer a question, not a model
// anything else is built on.

// AuditPersonData is one person's lists, as an entry point to the report.
type AuditPersonData struct {
	PersonID   string
	PersonName string
	// IsSelf marks the admin looking at their own lists, which is the case the
	// whole design is arranged around.
	IsSelf      bool
	IncludeOwn  bool
	ToggleIsOwn bool
	Lists       []AuditListRow
}

type AuditListRow struct {
	ID         string
	Name       string
	Visibility string
	Items      int
}

// AuditListData is the full state of one list.
type AuditListData struct {
	ListID     string
	ListName   string
	OwnerID    string
	OwnerName  string
	IsOwnList  bool
	Visibility string
	Items      []AuditItemRow
	// Drift counts items where claimed_qty disagrees with the claim rows — the
	// §2.1 invariant, per item rather than instance-wide.
	Drift int
}

type AuditItemRow struct {
	Title      string
	Quantity   int
	ClaimedQty int
	ClaimSum   int
	Drift      bool
	Removed    bool
	Added      string
	Claims     []AuditClaimRow
}

type AuditClaimRow struct {
	ClaimerName string
	Qty         int
	State       string
	// HasNote says a note exists. The text is never carried here: see the
	// handler for why.
	HasNote   bool
	CreatedAt string
	UpdatedAt string
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
