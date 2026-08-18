// Package model holds the persisted entity types shared by the store and the
// web layer. It deliberately contains no owner-blindness logic: the view
// models in internal/view are the only thing templates are allowed to see.
package model

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors used across the store and web layers.
var (
	ErrNotFound   = errors.New("not found")
	ErrForbidden  = errors.New("forbidden")
	ErrConflict   = errors.New("conflict")
	ErrOwnerBlind = errors.New("owner is blind to claims on their own list")
)

// Visibility values for lists (plan §3.1).
const (
	VisibilityPrivate  = "private"
	VisibilityAllUsers = "all_users"
	VisibilitySelected = "selected"
)

// Claim states (plan §3.3).
const (
	ClaimStateClaimed   = "claimed"
	ClaimStatePurchased = "purchased"
)

// Link statuses (plan §5.4).
const (
	LinkUnknown = "unknown"
	LinkOK      = "ok"
	LinkSuspect = "suspect"
	LinkDead    = "dead"
)

// Theme is the palette an account wears. It is stored on the user row: a
// preference belongs to the person, not to the browser they happen to be on.
//
// The values are a closed set clamped by ParseTheme, so an unknown one — an
// account written by an older build, a hand-edited row, a form field somebody
// typed into — renders as the default rather than as an unstyled page.
type Theme string

const (
	// ThemeForest is the brand green, and the default for every account and for
	// anyone not signed in yet.
	ThemeForest    Theme = "forest"
	ThemeCranberry Theme = "cranberry"
	ThemeNavy      Theme = "navy"
)

// Themes is every palette on offer, in the order the picker shows them. Adding
// one means adding it here and adding its two blocks to app.css; the stylesheet
// test checks the two stay in step.
var Themes = []Theme{ThemeForest, ThemeCranberry, ThemeNavy}

// ParseTheme accepts a known palette and answers ThemeForest for anything else.
func ParseTheme(s string) Theme {
	candidate := Theme(strings.TrimSpace(s))
	for _, t := range Themes {
		if t == candidate {
			return t
		}
	}
	return ThemeForest
}

// ItemSort is the order a list is read in.
//
// Unlike everything else in this file it is not a stored column: a sort is a
// request-scoped view option, carried in the query string. It is named here
// because three layers need the same vocabulary — the store turns it into an
// ORDER BY, the web layer parses it out of a URL, and the templates label it.
//
// Every key a sort can use is owner-authored: a price, a date the owner added
// something, a category they chose. None of them is claim-derived, which is
// what makes sorting safe to offer on an owner's own list (plan §3.2).
type ItemSort string

const (
	// SortManual is the owner's own order, the default everywhere.
	SortManual    ItemSort = "manual"
	SortPriceAsc  ItemSort = "price-asc"
	SortPriceDesc ItemSort = "price-desc"
	SortNewest    ItemSort = "added-new"
	SortOldest    ItemSort = "added-old"
	SortCategory  ItemSort = "category"
)

// ParseItemSort accepts one of the known orders and answers SortManual for
// everything else, so a hand-edited or stale URL degrades to the default rather
// than failing.
func ParseItemSort(s string) ItemSort {
	switch ItemSort(strings.TrimSpace(s)) {
	case SortPriceAsc:
		return SortPriceAsc
	case SortPriceDesc:
		return SortPriceDesc
	case SortNewest:
		return SortNewest
	case SortOldest:
		return SortOldest
	case SortCategory:
		return SortCategory
	default:
		return SortManual
	}
}

// NewID returns a UUIDv7 string: time-sortable, no sequence coordination.
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// NewV7 only fails if crypto/rand fails, which is not recoverable.
		panic("wishbone: uuid v7: " + err.Error())
	}
	return id.String()
}

// Now returns the current UTC time truncated to seconds, the granularity we
// store.
func Now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// TimeString renders a timestamp the way every column in the schema stores it.
func TimeString(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ParseTime parses a stored timestamp.
func ParseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

type User struct {
	ID           string
	Username     string
	DisplayName  string
	PasswordHash string
	IsAdmin      bool
	MustReset    bool
	Theme        Theme
	CreatedAt    string
	LegacyID     *string
}

type Session struct {
	TokenHash string
	UserID    string
	CreatedAt string
	ExpiresAt string
	UserAgent *string
}

type Invite struct {
	TokenHash string
	CreatedBy string
	CreatedAt string
	ExpiresAt string
	UsedAt    *string
	UsedBy    *string
}

type List struct {
	ID         string
	OwnerID    string
	Name       string
	Visibility string
	CreatedAt  string
	UpdatedAt  string
}

type Category struct {
	ID          string
	Slug        string
	Label       string
	SortOrder   int
	FieldSchema string // raw JSON; parse with categories.ParseFieldSchema
}

// Item is the raw row. claimed_qty is present here because the store needs it;
// it must never reach a template except through internal/view.
type Item struct {
	ID            string
	ListID        string
	CategoryID    *string
	Title         string
	URL           *string
	URLRaw        *string
	Description   *string
	Notes         *string
	PriceCents    *int64
	Currency      *string
	PriceSeenAt   *string
	Quantity      int
	ClaimedQty    int
	Attributes    string
	FieldSources  string
	LinkStatus    string
	LinkCheckedAt *string
	SortOrder     int
	CreatedAt     string
	UpdatedAt     string
	EditedAt      *string
	DeletedAt     *string
	LegacyID      *string
}

type ItemImage struct {
	ID        string
	ItemID    string
	SHA256    string
	Mime      string
	Width     *int
	Height    *int
	IsPrimary bool
	CreatedAt string
}

type Claim struct {
	ID          string
	ItemID      string
	ClaimerID   string
	ClaimerName string // joined for display to other claimers
	Qty         int
	State       string
	Note        *string
	CreatedAt   string
	UpdatedAt   string
}

// Ptr is a small helper for the many nullable columns in the schema.
func Ptr[T any](v T) *T { return &v }

// Deref returns the pointed-to value or the zero value.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
