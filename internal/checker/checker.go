// Package checker fetches the watched page and extracts the service
// notice block whose text is monitored for changes.
package checker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const maxBodyBytes = 5 << 20 // 5 MB

// Sentinel notice values. Both participate in comparisons like any other
// notice text: a notice block disappearing or emptying is itself a
// meaningful change (e.g. the disruption notice was taken down). They also
// guarantee ExtractNotice never returns "", which the engine reserves for
// "no baseline captured yet".
const (
	// NoticeMissing is used when the page contains no notice block at all.
	NoticeMissing = "(no notice block on page)"
	// NoticeEmpty is used when notice blocks exist but contain no text.
	NoticeEmpty = "(notice block is empty)"
)

const userAgent = "passport-check/1.0 (personal availability monitor)"

// noticeSelector targets the "Aktuelle Hinweise zu dieser Dienstleistung"
// block on service.berlin.de detail pages.
const noticeSelector = "#layout-grid__area--maincontent div.message"

// standRe matches the "[Stand: 24.08.2026 08:30]" freshness marker the site
// stamps on notice text. It is refreshed whenever the page is republished,
// including when the wording is untouched, so it is removed before the text
// is compared — otherwise a bumped timestamp alone would fire a change
// alert. It can appear in any block, not only the last one. The body is
// bounded (no nested "[", at most 80 runes) so a malformed marker — e.g. an
// unclosed bracket in the hand-maintained markup — fails to match instead
// of swallowing real notice text up to the next "]"; the failure mode is
// then a false-positive alert, never a silently suppressed one.
var standRe = regexp.MustCompile(`(?i)\[\s*stand\s*:[^\][]{0,80}\]`)

// Result describes one successful fetch of the page.
type Result struct {
	Notice     string // normalized notice text with volatile markers stripped, or a sentinel
	HTTPStatus int
	Duration   time.Duration
}

// Checker fetches and parses the target page.
type Checker struct {
	url    string
	client *http.Client
	log    *slog.Logger
}

// New builds a Checker for the given page URL.
func New(url string, log *slog.Logger) *Checker {
	return &Checker{
		url:    url,
		client: &http.Client{Timeout: 30 * time.Second},
		log:    log,
	}
}

// Fetch downloads the page and extracts the notice text. A non-2xx status
// or transport problem is returned as an error (a "fetch failure"); a
// missing notice block is not an error.
func (c *Checker) Fetch(ctx context.Context) (Result, error) {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return Result{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")

	resp, err := c.client.Do(req)
	if err != nil {
		c.log.Warn("fetch failed", "url", c.url, "err", err, "duration", time.Since(start))
		return Result{}, fmt.Errorf("fetching page: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		c.log.Warn("fetch returned non-2xx", "url", c.url, "status", resp.StatusCode)
		return Result{}, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	// The real page is ~60KB; the cap only guards against a misbehaving
	// server or captive portal streaming unbounded data.
	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return Result{}, fmt.Errorf("parsing HTML: %w", err)
	}

	res := Result{
		Notice:     ExtractNotice(doc),
		HTTPStatus: resp.StatusCode,
		Duration:   time.Since(start),
	}
	c.log.Debug("fetch ok",
		"url", c.url,
		"status", res.HTTPStatus,
		"duration", res.Duration,
		"notice_len", len(res.Notice),
	)
	return res, nil
}

// ExtractNotice pulls the normalized text of all notice blocks from a
// parsed document, joined in page order. Reading every block (rather than
// only the first) keeps the comparison meaningful even if the site ever
// injects an additional div.message banner above the real notice.
// Exported for tests against fixture HTML.
func ExtractNotice(doc *goquery.Document) string {
	sel := doc.Find(noticeSelector)
	if sel.Length() == 0 {
		return NoticeMissing
	}
	var parts []string
	sel.Each(func(_ int, s *goquery.Selection) {
		// Normalize before stripping: it folds non-ASCII whitespace (e.g.
		// NBSP, common on this site) to plain spaces, which standRe's
		// ASCII-only \s would otherwise not match inside the marker.
		if t := StripVolatile(Normalize(s.Text())); t != "" {
			parts = append(parts, t)
		}
	})
	if len(parts) == 0 {
		return NoticeEmpty
	}
	return strings.Join(parts, " ")
}

// Normalize collapses all runs of whitespace to single spaces so that
// cosmetic reformatting of the page does not register as a change.
func Normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// StripVolatile removes markers that the site changes without the notice
// itself meaning anything different — currently only "[Stand: ...]". The
// result is always whitespace-normalized (removing a marker leaves stray
// spaces behind). A block consisting solely of such a marker collapses to
// "" and is then dropped by ExtractNotice.
func StripVolatile(s string) string {
	return Normalize(standRe.ReplaceAllString(s, " "))
}
