package opendota

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type RateLimits struct {
	ServerTime                                           *time.Time
	RemainingMinute, LimitMinute, RemainingDay, LimitDay *int64
	RecommendedPause                                     time.Duration
}

func intHeader(h http.Header, key string) *int64 {
	v, err := strconv.ParseInt(h.Get(key), 10, 64)
	if err != nil {
		return nil
	}
	return &v
}
func ParseLimits(h http.Header) *RateLimits {
	r := &RateLimits{RemainingMinute: intHeader(h, "X-Rate-Limit-Remaining-Minute"), LimitMinute: intHeader(h, "X-Rate-Limit-Limit-Minute"), RemainingDay: intHeader(h, "X-Rate-Limit-Remaining-Day"), LimitDay: intHeader(h, "X-Rate-Limit-Limit-Day")}
	at, err := http.ParseTime(h.Get("Date"))
	if err == nil {
		at = at.UTC()
		r.ServerTime = &at
	}
	if r.ServerTime == nil && r.RemainingMinute == nil && r.LimitMinute == nil && r.RemainingDay == nil && r.LimitDay == nil {
		return nil
	}
	if r.ServerTime == nil {
		return r
	}
	if r.RemainingMinute != nil && *r.RemainingMinute < 10 {
		delay := max(at.Truncate(time.Minute).Add(time.Minute).Sub(at), time.Second)
		if *r.RemainingMinute > 0 {
			delay /= time.Duration(*r.RemainingMinute)
		}
		r.RecommendedPause = max(r.RecommendedPause, delay)
	}
	if r.RemainingDay != nil && *r.RemainingDay < 100 {
		next := time.Date(at.Year(), at.Month(), at.Day()+1, 0, 0, 0, 0, time.UTC)
		delay := max(next.Sub(at), time.Second)
		if *r.RemainingDay > 0 {
			delay /= time.Duration(*r.RemainingDay)
		}
		r.RecommendedPause = max(r.RecommendedPause, delay)
	}
	return r
}
func retryAfter(h http.Header, now time.Time) time.Duration {
	text := strings.TrimSpace(h.Get("Retry-After"))
	if seconds, err := strconv.ParseInt(text, 10, 64); err == nil && seconds > 0 {
		if seconds > int64((365*24*time.Hour)/time.Second) {
			return 365 * 24 * time.Hour
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(text); err == nil {
		if server, err := http.ParseTime(h.Get("Date")); err == nil {
			now = server
		}
		return max(at.Sub(now), 0)
	}
	return 0
}
func copyLimits(r *RateLimits) *RateLimits {
	if r == nil {
		return nil
	}
	c := *r
	copyInt := func(p *int64) *int64 {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	c.RemainingMinute = copyInt(r.RemainingMinute)
	c.LimitMinute = copyInt(r.LimitMinute)
	c.RemainingDay = copyInt(r.RemainingDay)
	c.LimitDay = copyInt(r.LimitDay)
	if r.ServerTime != nil {
		at := *r.ServerTime
		c.ServerTime = &at
	}
	return &c
}
