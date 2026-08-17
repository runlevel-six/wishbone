// Package linkcheck re-checks stored product links on a schedule (plan §5.4).
//
// link_status is written once, when an item is created from a URL, and then
// never revisited. Wishlists outlive the things on them: a link that was good in
// August is a discontinued product in November, and the person who finds out is
// whoever tried to buy it. This job is what turns that into something the list
// owner sees first.
//
// Three decisions carry the whole design.
//
// **It reuses the extraction pipeline rather than asking for a status code.**
// A dead product link frequently answers 200 — the retailer redirects to a
// collection page, or serves a shell that says nothing — which is exactly what
// the soft-404 guard exists to catch, and it needs a real extraction to judge.
// Going through extract.Service also means this job inherits every fix made
// there for free: the canonical follow-through, the streamed redirect, the
// blocked-is-not-dead distinction.
//
// **Only the shop saying the thing is gone marks a link dead.** 404 and 410 are
// evidence. 403, 429, a timeout, a DNS failure, or a page the guard merely
// distrusts are not evidence about the link — they are evidence about this
// request — and writing them into link_status would tell people their good links
// are broken. Those outcomes update link_checked_at and leave the status alone.
//
// **It is slow on purpose.** A job walking every item from one address is the
// traffic shape bot detection scores hardest, and the fetcher's own notes are
// explicit that a cold request succeeding says nothing about sustained polling.
// So: off unless enabled, a small batch, a pause between items, and only items
// nobody has checked in a week. Finishing quickly is worth nothing here.
//
// What this job must never do (plan §3.2): surface anything claim-derived. It
// reads items and writes link_status. It does not touch claims, and nothing it
// writes varies with whether an item is claimed, so a claimer's interest cannot
// leak through it.
package linkcheck

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"wishbone/internal/extract"
	"wishbone/internal/fetch"
	"wishbone/internal/model"
	"wishbone/internal/store"
)

// Options is everything the job needs from configuration.
type Options struct {
	Interval time.Duration
	Batch    int
	Age      time.Duration
	Spacing  time.Duration
}

type Checker struct {
	st   *store.Store
	ex   *extract.Service
	log  *slog.Logger
	opts Options

	// sleep is time.Sleep in production. Tests replace it: the pacing is part of
	// the design, so it has to be observable without a test taking minutes.
	sleep func(time.Duration)
	// now is model.Now in production, for the same reason.
	now func() time.Time
}

func New(st *store.Store, ex *extract.Service, log *slog.Logger, opts Options) *Checker {
	if opts.Batch <= 0 {
		opts.Batch = 20
	}
	return &Checker{
		st:    st,
		ex:    ex,
		log:   log,
		opts:  opts,
		sleep: time.Sleep,
		now:   model.Now,
	}
}

// Run sweeps on a ticker until the context is canceled. It does not sweep on
// startup: a restart loop would otherwise turn into a request loop, and there is
// nothing urgent about a link that has been dead for a week.
func (c *Checker) Run(ctx context.Context) {
	if !c.ex.Enabled() {
		c.log.Warn("link health job not started: URL fetching is disabled")
		return
	}
	c.log.Info("link health job started",
		slog.Duration("interval", c.opts.Interval),
		slog.Int("batch", c.opts.Batch),
		slog.Duration("age", c.opts.Age),
		slog.Duration("spacing", c.opts.Spacing))

	ticker := time.NewTicker(c.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Sweep(ctx)
		}
	}
}

// Result counts one sweep, for the log line and for tests.
type Result struct {
	Checked      int
	OK           int
	Dead         int
	Suspect      int
	Inconclusive int
}

// Sweep checks one batch. Exported so a sweep can be triggered in a test, and so
// a future `wishbone check-links` subcommand has something to call.
func (c *Checker) Sweep(ctx context.Context) Result {
	var res Result

	before := model.TimeString(c.now().Add(-c.opts.Age))
	items, err := c.st.ItemsDueForLinkCheck(ctx, before, c.opts.Batch)
	if err != nil {
		c.log.Error("link health: selecting items", slog.Any("err", err))
		return res
	}
	if len(items) == 0 {
		return res
	}

	items, deferred := spreadByHost(items, c.perHostCap())
	if deferred > 0 {
		// Said out loud: a cap that silently drops work reads as "everything was
		// checked" when it was not. The deferred items keep their old
		// link_checked_at, so they are first in line next sweep.
		c.log.Info("link health: deferring items to spread the load",
			slog.Int("deferred", deferred), slog.Int("per_host_cap", c.perHostCap()))
	}

	for i, it := range items {
		if ctx.Err() != nil {
			return res
		}
		// Between items, not before the first: the pause is there to keep this
		// from looking like a crawl, and there is nothing to space out yet.
		if i > 0 {
			c.sleep(c.opts.Spacing)
		}
		res.Checked++
		c.check(ctx, it, &res)
	}

	c.log.Info("link health sweep",
		slog.Int("checked", res.Checked),
		slog.Int("ok", res.OK),
		slog.Int("dead", res.Dead),
		slog.Int("suspect", res.Suspect),
		slog.Int("inconclusive", res.Inconclusive))
	return res
}

// perHostCap is the most items from one host a single sweep will touch.
//
// A quarter of the batch, never fewer than two. The reason is a measured mix
// rather than a principle: 60% of the first real corpus pointed at one
// marketplace — the one with the most aggressive bot detection and the one the
// phone share sheet produces links for. Without a cap, the age floor makes
// everything come due together and a sweep becomes twelve requests to that host
// inside ten minutes, which is denser than anything the interactive path does.
//
// And the interactive path is what this protects. Both share an egress address,
// so a job that teaches a filter to distrust it costs somebody pasting a link on
// their phone, to gain a re-check of a page that marketplace almost never takes
// down. Cheap insurance against a mix nobody chose.
func (c *Checker) perHostCap() int {
	if cap := c.opts.Batch / 4; cap >= 2 {
		return cap
	}
	return 2
}

// spreadByHost caps how many items one host contributes and interleaves what is
// left, so consecutive requests go to different places wherever possible. It
// returns the reordered items and how many were held back.
//
// Order within a host is preserved, so the oldest check is still the first to be
// looked at — the cap changes how much of one host a sweep takes, not which of
// its items go first.
func spreadByHost(items []*model.Item, cap int) (out []*model.Item, deferred int) {
	byHost := map[string][]*model.Item{}
	var order []string
	for _, it := range items {
		h := hostOf(model.Deref(it.URL))
		if _, seen := byHost[h]; !seen {
			order = append(order, h)
		}
		byHost[h] = append(byHost[h], it)
	}
	for _, h := range order {
		if len(byHost[h]) > cap {
			deferred += len(byHost[h]) - cap
			byHost[h] = byHost[h][:cap]
		}
	}
	// Round-robin across hosts: with one host dominating, its requests end up as
	// far apart as the batch allows.
	for n := 0; len(out) < len(items)-deferred; n++ {
		progressed := false
		for _, h := range order {
			if n < len(byHost[h]) {
				out = append(out, byHost[h][n])
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out, deferred
}

// hostOf is the registrable-ish host of a stored URL: enough to tell one
// retailer from another, and deliberately not clever about it.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return strings.TrimPrefix(strings.ToLower(u.Host), "www.")
}

func (c *Checker) check(ctx context.Context, it *model.Item, res *Result) {
	url := model.Deref(it.URL)
	checkedAt := model.TimeString(c.now())

	prev, err := c.ex.Fetch(ctx, url)
	switch {
	case err != nil:
		// A body the fetcher would not parse is still a status worth reading. A
		// 404 page is frequently not HTML — plenty are empty — and "the shop says
		// it is gone" is the whole point of this job.
		var cte *fetch.ContentTypeError
		if errors.As(err, &cte) && gone(cte.StatusCode) {
			res.Dead++
			c.record(ctx, it, model.LinkDead, checkedAt)
			return
		}
		// Otherwise the failure says nothing about the link. Record that the
		// attempt happened so the same item is not retried every sweep, and
		// leave the status as it was.
		res.Inconclusive++
		c.touch(ctx, it, checkedAt)
		c.log.Debug("link health: inconclusive",
			slog.String("item_id", it.ID), slog.Any("err", err))
		return

	case prev.Blocked():
		// The shop refused to talk to us. Nothing was learned about the link,
		// and calling it dead would be a lie about somebody's good link.
		res.Inconclusive++
		c.touch(ctx, it, checkedAt)
		c.log.Debug("link health: refused by the shop",
			slog.String("item_id", it.ID), slog.Int("status", prev.StatusCode))
		return
	}

	status := prev.LinkStatus
	switch status {
	case model.LinkDead:
		res.Dead++
	case model.LinkSuspect:
		res.Suspect++
	case model.LinkOK:
		res.OK++
	default:
		// Nothing conclusive either way; leave what is stored alone.
		res.Inconclusive++
		c.touch(ctx, it, checkedAt)
		return
	}

	c.record(ctx, it, status, checkedAt)
}

// gone reports the two statuses that are evidence about a link rather than about
// this request: the shop saying the thing is not there.
func gone(status int) bool {
	return status == http.StatusNotFound || status == http.StatusGone
}

func (c *Checker) record(ctx context.Context, it *model.Item, status, at string) {
	if err := c.st.SetLinkStatus(ctx, it.ID, status, at); err != nil {
		c.log.Error("link health: recording status",
			slog.String("item_id", it.ID), slog.Any("err", err))
		return
	}
	if status != it.LinkStatus {
		c.log.Info("link health: status changed",
			slog.String("item_id", it.ID),
			slog.String("from", it.LinkStatus),
			slog.String("to", status))
	}
}

// touch records that the link was looked at without claiming anything about it.
func (c *Checker) touch(ctx context.Context, it *model.Item, at string) {
	if err := c.st.SetLinkStatus(ctx, it.ID, it.LinkStatus, at); err != nil {
		c.log.Error("link health: recording attempt",
			slog.String("item_id", it.ID), slog.Any("err", err))
	}
}
