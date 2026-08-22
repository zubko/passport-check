package notify

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

	c := NewWithURL(srv.URL, "tok", "usr", 10*time.Minute, discardLogger())
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

func TestSendEmergencyAddsRetryExpire(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = map[string]string{
			"priority": r.PostForm.Get("priority"),
			"retry":    r.PostForm.Get("retry"),
			"expire":   r.PostForm.Get("expire"),
			"sound":    r.PostForm.Get("sound"),
		}
		_, _ = w.Write([]byte(`{"status":1,"request":"abc123"}`))
	}))
	defer srv.Close()

	c := NewWithURL(srv.URL, "tok", "usr", 10*time.Minute, discardLogger())
	err := c.Send(context.Background(), Message{Title: "T", Body: "B", Priority: PriorityEmergency, Sound: SoundPositive})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got["priority"] != "2" || got["retry"] != "60" || got["expire"] != "600" {
		t.Errorf("emergency params = %v, want priority=2 retry=60 expire=600", got)
	}
	if got["sound"] != SoundPositive {
		t.Errorf("form[sound] = %q, want %q", got["sound"], SoundPositive)
	}
}

func TestEmergencyExpireFollowsAlertIntervalClamped(t *testing.T) {
	cases := []struct {
		interval time.Duration
		want     string
	}{
		{time.Hour, "3600"},      // follows the configured cadence
		{10 * time.Second, "60"}, // clamped up to the retry period
		{5 * time.Hour, "10800"}, // clamped to the Pushover maximum (3h)
	}
	for _, tc := range cases {
		var gotExpire string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotExpire = r.PostForm.Get("expire")
			_, _ = w.Write([]byte(`{"status":1}`))
		}))
		c := NewWithURL(srv.URL, "tok", "usr", tc.interval, discardLogger())
		if err := c.Send(context.Background(), Message{Title: "T", Body: "B", Priority: PriorityEmergency}); err != nil {
			t.Fatalf("interval %s: Send: %v", tc.interval, err)
		}
		srv.Close()
		if gotExpire != tc.want {
			t.Errorf("interval %s: expire = %q, want %q", tc.interval, gotExpire, tc.want)
		}
	}
}

func TestSendAPIRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":0,"errors":["application token is invalid"]}`))
	}))
	defer srv.Close()

	c := NewWithURL(srv.URL, "bad", "usr", 10*time.Minute, discardLogger())
	err := c.Send(context.Background(), Message{Title: "T", Body: "B"})
	if err == nil {
		t.Fatal("want error on API rejection, got nil")
	}
	if want := "application token is invalid"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not mention %q", err, want)
	}
}
