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

func TestExtractNoticeFromFixture(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(fixture(t))))
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	notice := ExtractNotice(doc)

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
}

func TestExtractNoticeMissingBlock(t *testing.T) {
	html := `<html><body><div id="layout-grid__area--maincontent"><h1>Titel</h1></div></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if got := ExtractNotice(doc); got != NoticeMissing {
		t.Errorf("want NoticeMissing sentinel, got %q", got)
	}
}

func TestExtractNoticeEmptyBlockUsesSentinel(t *testing.T) {
	// An existing-but-empty block must NOT extract to "": the engine
	// reserves "" for "no baseline captured yet".
	html := `<html><body><div id="layout-grid__area--maincontent"><div class="message">   </div></div></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if got := ExtractNotice(doc); got != NoticeEmpty {
		t.Errorf("want NoticeEmpty sentinel, got %q", got)
	}
}

func TestExtractNoticeJoinsMultipleBlocks(t *testing.T) {
	// All message blocks are read so an injected banner above the real
	// notice cannot hide changes to it.
	html := `<html><body><div id="layout-grid__area--maincontent">
		<div class="message">Cookie Banner</div>
		<div class="message">Echte Störung</div>
	</div></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	got := ExtractNotice(doc)
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
