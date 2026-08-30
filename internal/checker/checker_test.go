package checker

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/page.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return data
}

func parse(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	return doc
}

func TestExtractNoticeFromFixture(t *testing.T) {
	notice := ExtractNotice(parse(t, string(fixture(t))))

	if notice == NoticeMissing {
		t.Fatal("notice block not found in fixture")
	}
	want := "Leider ist die Dienstleistung derzeit online nicht nutzbar."
	if !strings.Contains(notice, want) {
		t.Errorf("notice does not contain %q; got: %s", want, notice)
	}
	if strings.Contains(notice, "\n") || strings.Contains(notice, "  ") {
		t.Error("notice is not whitespace-normalized")
	}
	// The fixture carries a "[Stand: ...]" marker; extraction must drop it.
	if strings.Contains(notice, "Stand:") {
		t.Errorf("Stand marker survived extraction: %s", notice)
	}
}

func TestExtractNoticeStripsStandMarker(t *testing.T) {
	// The site refreshes "[Stand: ...]" on every republish, so it must not
	// reach the comparison: two pages differing only in that timestamp have
	// to extract to identical text.
	page := func(stand string) string {
		return `<html><body><div id="layout-grid__area--maincontent"><div class="message">
			<p>Leider ist die Dienstleistung derzeit online nicht nutzbar.<br />
			` + stand + `</p>
		</div></div></body></html>`
	}
	got := ExtractNotice(parse(t, page("[Stand: 24.08.2026 08:30]")))
	if want := "Leider ist die Dienstleistung derzeit online nicht nutzbar."; got != want {
		t.Errorf("notice = %q, want %q", got, want)
	}
	if later := ExtractNotice(parse(t, page("[Stand: 25.08.2026 17:05]"))); later != got {
		t.Errorf("timestamp bump changed the notice:\n old: %q\n new: %q", got, later)
	}
}

func TestStripVolatile(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Störung [Stand: 24.08.2026 08:30]", "Störung"},
		{"[Stand: 24.08.2026] Störung behoben", "Störung behoben"},
		{"vor [Stand: 1.1.2026] und [stand : 2.2.2026] nach", "vor und nach"},
		{"keine Marker hier", "keine Marker hier"},
		{"eckige [Klammern] bleiben", "eckige [Klammern] bleiben"},
		// The result is normalized even when no marker is present.
		{"kein  Marker,\n\taber Whitespace", "kein Marker, aber Whitespace"},
		// A malformed (unclosed/nested) marker must NOT swallow real text
		// up to the next "]"; it survives verbatim instead (worst case: a
		// false-positive alert, never a suppressed one).
		{"kaputt [Stand: 1.1.2026 Text [echt] mehr", "kaputt [Stand: 1.1.2026 Text [echt] mehr"},
	}
	for _, c := range cases {
		if got := StripVolatile(c.in); got != c.want {
			t.Errorf("StripVolatile(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractNoticeMissingBlock(t *testing.T) {
	html := `<html><body><div id="layout-grid__area--maincontent"><h1>Titel</h1></div></body></html>`
	if got := ExtractNotice(parse(t, html)); got != NoticeMissing {
		t.Errorf("want NoticeMissing sentinel, got %q", got)
	}
}

func TestExtractNoticeEmptyBlockUsesSentinel(t *testing.T) {
	// A block left without text must NOT extract to "": the engine
	// reserves "" for "no baseline captured yet".
	cases := []struct{ name, block string }{
		{"whitespace only", "   "},
		{"stand marker only", "[Stand: 24.08.2026 08:30]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			html := `<html><body><div id="layout-grid__area--maincontent"><div class="message">` + c.block + `</div></div></body></html>`
			if got := ExtractNotice(parse(t, html)); got != NoticeEmpty {
				t.Errorf("want NoticeEmpty sentinel, got %q", got)
			}
		})
	}
}

func TestExtractNoticeJoinsMultipleBlocks(t *testing.T) {
	// All message blocks are read so an injected banner above the real
	// notice cannot hide changes to it.
	html := `<html><body><div id="layout-grid__area--maincontent">
		<div class="message">Cookie Banner</div>
		<div class="message">Echte Störung</div>
	</div></body></html>`
	got := ExtractNotice(parse(t, html))
	if !strings.Contains(got, "Cookie Banner") || !strings.Contains(got, "Echte Störung") {
		t.Errorf("want both blocks in extracted notice, got %q", got)
	}
}

func TestFetchOK(t *testing.T) {
	page := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "passport-check/") {
			t.Errorf("unexpected User-Agent %q", got)
		}
		_, _ = w.Write(page)
	}))
	defer srv.Close()

	c := New(srv.URL, discardLogger())
	res, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.HTTPStatus != http.StatusOK {
		t.Errorf("status = %d, want 200", res.HTTPStatus)
	}
	if !strings.Contains(res.Notice, "technische Störung") {
		t.Errorf("unexpected notice: %s", res.Notice)
	}
}

func TestFetchNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "kaputt", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, discardLogger())
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("want error for HTTP 500, got nil")
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize("  a\n\t b   c \n")
	if got != "a b c" {
		t.Errorf("Normalize = %q, want %q", got, "a b c")
	}
}
