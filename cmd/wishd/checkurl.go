package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http/httptrace"
	"sort"
	"strings"
	"time"

	"wishd/internal/config"
	"wishd/internal/extract"
	"wishd/internal/fetch"
)

// checkURLCmd runs one URL through the real extraction pipeline and reports
// what happened, phase by phase.
//
// It exists because extraction can only be judged from inside the cluster: bot
// detection keys on source address and on the TLS and header fingerprint of
// the client, so a result from a laptop says nothing about a result from a
// pod. When a lookup fails in the UI, this says whether it was DNS, the
// address guard, the TCP connect, the TLS handshake, a bot block, or simply a
// page with no usable metadata.
//
//	kubectl run wishd-check --rm -it --restart=Never \
//	  --image=<your image> --labels=app.kubernetes.io/name=wishd \
//	  -- check-url https://example.com/products/thing
func checkURLCmd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("check-url", flag.ContinueOnError)
	var (
		ua      = fs.String("ua", "", "override the User-Agent")
		timeout = fs.Duration("timeout", 15*time.Second, "overall timeout for this check")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: wishd check-url [flags] <url>")
	}
	target := fs.Arg(0)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *ua != "" {
		cfg.FetchUserAgent = *ua
	}

	normalized, nerr := extract.NormalizeURL(target)
	fmt.Fprintf(stdout, "wishd       %s\n", version)
	fmt.Fprintf(stdout, "requested   %s\n", target)
	if nerr != nil {
		fmt.Fprintf(stdout, "normalized  FAILED: %v\n", nerr)
		return nil
	}
	fmt.Fprintf(stdout, "normalized  %s\n", normalized)
	fmt.Fprintf(stdout, "user-agent  %s\n", cfg.FetchUserAgent)
	fmt.Fprintf(stdout, "sidecar     %s\n\n", either(cfg.SidecarURL, "(not configured)"))

	client := fetch.New(fetch.Options{
		UserAgent:      cfg.FetchUserAgent,
		AcceptLanguage: cfg.FetchLang,
	})
	sidecar := extract.NewSidecar(cfg.SidecarURL, cfg.SidecarTimeout)
	svc := extract.NewService(client, sidecar, true)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// httptrace is honored by the transport straight from the request context,
	// so this measures the real fetch rather than a re-implementation of it.
	start := time.Now()
	var marks []mark
	at := func(what string) { marks = append(marks, mark{what, time.Since(start)}) }

	trace := &httptrace.ClientTrace{
		DNSStart:     func(i httptrace.DNSStartInfo) { at("dns lookup started: " + i.Host) },
		DNSDone:      func(i httptrace.DNSDoneInfo) { at("dns resolved: " + joinAddrs(i)) },
		ConnectStart: func(net, addr string) { at("tcp connecting: " + addr) },
		ConnectDone: func(net, addr string, err error) {
			if err != nil {
				at("tcp FAILED " + addr + ": " + err.Error())
				return
			}
			at("tcp connected: " + addr)
		},
		TLSHandshakeStart: func() { at("tls handshake started") },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err != nil {
				at("tls FAILED: " + err.Error())
				return
			}
			at("tls established")
		},
		GotFirstResponseByte: func() { at("first response byte") },
	}

	preview, ferr := svc.Fetch(httptrace.WithClientTrace(ctx, trace), normalized)
	elapsed := time.Since(start)

	fmt.Fprintln(stdout, "timeline")
	for _, m := range marks {
		fmt.Fprintf(stdout, "  %8.3fs  %s\n", m.at.Seconds(), m.what)
	}
	fmt.Fprintf(stdout, "  %8.3fs  finished\n\n", elapsed.Seconds())

	if ferr != nil {
		fmt.Fprintf(stdout, "RESULT: fetch failed: %v\n\n", ferr)
		switch {
		case errors.Is(ferr, fetch.ErrBlockedAddress):
			fmt.Fprintln(stdout, "The address guard refused the destination. That is the SSRF")
			fmt.Fprintln(stdout, "protection working; the host resolved to a private or reserved address.")
		case strings.Contains(ferr.Error(), "TLS handshake timeout"):
			fmt.Fprintln(stdout, "The TCP connection was accepted but the TLS handshake never completed.")
			fmt.Fprintln(stdout, "That is characteristic of edge-level bot filtering: the client's TLS")
			fmt.Fprintln(stdout, "fingerprint is rejected before any HTTP is exchanged, so no user-agent")
			fmt.Fprintln(stdout, "change will help. The manual form and the sidecar are the options.")
		case strings.Contains(ferr.Error(), "i/o timeout"), errors.Is(ferr, context.DeadlineExceeded):
			fmt.Fprintln(stdout, "Timed out. If the timeline shows no 'tcp connected' line, egress is")
			fmt.Fprintln(stdout, "blocked or the host is dropping packets from this address.")
		case errors.Is(ferr, fetch.ErrContentType):
			fmt.Fprintln(stdout, "The response was not HTML — often a bot-check or a redirect to one.")
		}
		return nil
	}

	res := preview.Result
	fmt.Fprintf(stdout, "RESULT: fetched, final URL %s\n", preview.URL)
	fmt.Fprintf(stdout, "  link status  %s\n", res.LinkStatus)
	if res.Suspect {
		fmt.Fprintln(stdout, "  SUSPECT — nothing would be auto-filled:")
		for _, r := range res.SuspectReason {
			fmt.Fprintf(stdout, "    - %s\n", r)
		}
	}
	fmt.Fprintf(stdout, "  title        %s\n", either(res.Title, "(none)"))
	fmt.Fprintf(stdout, "  price        %s %s\n", centsString(res.PriceCents), res.Currency)
	fmt.Fprintf(stdout, "  sku / brand  %s / %s\n", either(res.SKU, "-"), either(res.Brand, "-"))
	fmt.Fprintf(stdout, "  og:type      %s\n", either(res.OGType, "(none)"))
	fmt.Fprintf(stdout, "  images       %d\n", len(res.ImageURLs))
	if len(res.Attributes) > 0 {
		fmt.Fprintf(stdout, "  attributes   %s\n", kvString(res.Attributes))
	}
	fmt.Fprintf(stdout, "  tiers run    %s\n", strings.Join(res.Tried, ", "))
	if len(res.Sources) > 0 {
		fmt.Fprintf(stdout, "  field sources %s\n", kvString(res.Sources))
	}
	for name, msg := range res.Errors {
		fmt.Fprintf(stdout, "  tier %-10s declined: %s\n", name, msg)
	}
	return nil
}

type mark struct {
	what string
	at   time.Duration
}

func joinAddrs(i httptrace.DNSDoneInfo) string {
	if i.Err != nil {
		return "FAILED: " + i.Err.Error()
	}
	var out []string
	for _, a := range i.Addrs {
		out = append(out, a.IP.String())
	}
	return strings.Join(out, ", ")
}

func kvString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, " ")
}

func centsString(c *int64) string {
	if c == nil {
		return "(none)"
	}
	return fmt.Sprintf("%d.%02d", *c/100, *c%100)
}

func either(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
