package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReadinessRequiresProgressAndDependencies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	dependency := error(nil)
	m, err := Start(ctx, "", "worker", func(context.Context) error { return dependency })
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return now }
	probe := func(want int) {
		t.Helper()
		r := httptest.NewRecorder()
		m.serve(r, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if r.Code != want {
			t.Fatalf("status %d: %s", r.Code, r.Body.String())
		}
	}
	probe(503) // An idle timer or a healthy DB cannot claim a completed cycle.
	m.Begin()
	m.Progress()
	probe(503)
	m.Complete(nil, 15*time.Minute)
	probe(200)
	now = now.Add(16 * time.Minute)
	probe(200) // Expected idle interval is included in the deadline.
	m.Begin()
	now = now.Add(ProgressTimeout)
	probe(503) // A stalled active cycle is not hidden by the idle interval.
	m.Progress()
	probe(200)
	m.Complete(errors.New("token=secret"), time.Minute)
	m.Progress()
	probe(503) // Checkpoints alone cannot erase a failed cycle.
	m.Complete(nil, time.Minute)
	dependency = errors.New("dsn=secret")
	probe(503)
	dependency = nil
	probe(200)
	cancel()
	probe(503)
}

func TestProcessProbeIdentityAndShutdown(t *testing.T) {
	m, err := Start(context.Background(), "127.0.0.1:0", "bot", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	if err := Probe(ctx, m.Address(), "bot"); err == nil {
		t.Fatal("startup reported ready")
	}
	m.Complete(nil, 0)
	if err := Probe(ctx, m.Address(), "bot"); err != nil {
		t.Fatal(err)
	}
	if err := Probe(ctx, m.Address(), "worker"); err == nil {
		t.Fatal("wrong component accepted")
	}
	m.Close()
	if err := Probe(ctx, m.Address(), "bot"); err == nil {
		t.Fatal("closed process reported ready")
	}
}

func TestReadinessAddressIsLoopbackOnly(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1:8080", "[::1]:8080"} {
		if err := ValidateAddress(addr); err != nil {
			t.Fatal(addr, err)
		}
	}
	for _, addr := range []string{":8080", "0.0.0.0:8080", "192.168.1.1:8080", "localhost:8080", "127.0.0.1:-1", "127.0.0.1:65536", "secret"} {
		if err := ValidateAddress(addr); err == nil {
			t.Fatal("accepted", addr)
		}
	}
	if err := Probe(context.Background(), "", "bot"); err == nil {
		t.Fatal("probe accepted disabled readiness")
	}
}
