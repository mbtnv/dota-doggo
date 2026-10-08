package opendota

import (
	"net/http"
	"testing"
	"time"
)

func TestRateLimits(t *testing.T) {
	for _, tc := range []struct {
		minute, day string
		pause       time.Duration
	}{{"2", "2744", 3 * time.Second}, {"0", "2744", 6 * time.Second}, {"54", "2", 28743 * time.Second}} {
		h := http.Header{}
		h.Set("Date", "Wed, 11 Mar 2026 08:01:54 GMT")
		h.Set("X-Rate-Limit-Remaining-Minute", tc.minute)
		h.Set("X-Rate-Limit-Remaining-Day", tc.day)
		h.Set("X-Rate-Limit-Limit-Minute", "60")
		h.Set("X-Rate-Limit-Limit-Day", "50000")
		r := ParseLimits(h)
		if r == nil || r.RecommendedPause != tc.pause || r.LimitMinute == nil || *r.LimitMinute != 60 || r.LimitDay == nil || *r.LimitDay != 50000 {
			t.Fatalf("%#v -> %#v", tc, r)
		}
	}
	h := http.Header{}
	h.Set("X-Rate-Limit-Remaining-Minute", "1")
	r := ParseLimits(h)
	if r.RecommendedPause != 0 || r.ServerTime != nil {
		t.Fatal(r)
	}
	if ParseLimits(http.Header{}) != nil {
		t.Fatal("invented limits")
	}
	h.Set("X-Rate-Limit-Remaining-Minute", "garbage")
	if ParseLimits(h) != nil {
		t.Fatal("accepted invalid header")
	}
}

func TestRetryAfterDateUsesServerClock(t *testing.T) {
	h := http.Header{}
	h.Set("Date", "Wed, 11 Mar 2026 08:01:54 GMT")
	h.Set("Retry-After", "Wed, 11 Mar 2026 08:02:00 GMT")
	if got := retryAfter(h, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)); got != 6*time.Second {
		t.Fatal(got)
	}
}
