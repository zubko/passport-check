package notify

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestSendNormalPriority(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = map[string]string{}
		for k := range r.PostForm {
			got[k] = r.PostForm.Get(k)
		}
		_, _ = w.Write([]byte(`{"status":1,"request":"abc123"}`))
	}))
	defer srv.Close()

	c := NewWithURL(srv.URL, "tok", "usr", discardLogger())
	err := c.Send(context.Background(), Message{Title: "T", Body: "B", Priority: PriorityNormal})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	for k, want := range map[string]string{
		"token": "tok", "user": "usr", "title": "T", "message": "B", "priority": "0",
	} {
		if got[k] != want {
			t.Errorf("form[%s] = %q, want %q", k, got[k], want)
		}
	}
	if _, ok := got["retry"]; ok {
		t.Error("retry must not be set for priority 0")
	}
	if _, ok := got["sound"]; ok {
		t.Error("sound must not be set when Message.Sound is empty")
	}
}

func TestSendHighPriority(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = map[string]string{}
		for k := range r.PostForm {
			got[k] = r.PostForm.Get(k)
		}
		_, _ = w.Write([]byte(`{"status":1,"request":"abc123"}`))
	}))
	defer srv.Close()

	c := NewWithURL(srv.URL, "tok", "usr", discardLogger())
	err := c.Send(context.Background(), Message{Title: "T", Body: "B", Priority: PriorityHigh, Sound: SoundPositive})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got["priority"] != "1" {
		t.Errorf("form[priority] = %q, want %q", got["priority"], "1")
	}
	if got["sound"] != SoundPositive {
		t.Errorf("form[sound] = %q, want %q", got["sound"], SoundPositive)
	}
	// The app repeats alerts itself, so it never uses Pushover's
	// emergency acknowledge-or-retry loop.
	for _, k := range []string{"retry", "expire"} {
		if _, ok := got[k]; ok {
			t.Errorf("form[%s] must not be set", k)
		}
	}
}

func TestSendAPIRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":0,"errors":["application token is invalid"]}`))
	}))
	defer srv.Close()

	c := NewWithURL(srv.URL, "bad", "usr", discardLogger())
	err := c.Send(context.Background(), Message{Title: "T", Body: "B"})
	if err == nil {
		t.Fatal("want error on API rejection, got nil")
	}
	if want := "application token is invalid"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not mention %q", err, want)
	}
}
