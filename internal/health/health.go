// Package health reports process readiness from actual loop progress and dependencies.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const ProgressTimeout = 2 * time.Minute

// ValidateAddress restricts the optional diagnostics listener to loopback.
func ValidateAddress(addr string) error {
	if addr == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 0 || n > 65535 || ip == nil || !ip.IsLoopback() {
		return errors.New("HEALTH_ADDR must be a loopback IP:port")
	}
	return nil
}

type Monitor struct {
	mu        sync.Mutex
	now       func() time.Time
	ready     bool
	deadline  time.Time
	component string
	ctx       context.Context
	check     func(context.Context) error
	server    *http.Server
	listener  net.Listener
	done      chan struct{}
}

// Start is optional when addr is empty. A listener starts unready, even if the
// database is available. No timer can mark a stalled loop as ready.
func Start(ctx context.Context, addr, component string, check func(context.Context) error) (*Monitor, error) {
	if err := ValidateAddress(addr); err != nil {
		return nil, err
	}
	m := &Monitor{now: time.Now, component: component, ctx: ctx, check: check}
	if addr == "" {
		return m, nil
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("start readiness listener: %w", err)
	}
	m.listener = l
	m.server = &http.Server{Handler: http.HandlerFunc(m.serve), ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second}
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		_ = m.server.Serve(l)
		m.mu.Lock()
		m.ready = false
		m.mu.Unlock()
	}()
	return m, nil
}

// Begin bounds an active cycle; Progress extends it only at real checkpoints.
// A failed cycle stays unready until a subsequent cycle succeeds.
func (m *Monitor) Begin() { m.Progress() }
func (m *Monitor) Progress() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deadline = m.now().Add(ProgressTimeout)
}
func (m *Monitor) Complete(err error, idle time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ready = err == nil
	m.deadline = m.now().Add(idle).Add(ProgressTimeout)
}
func (m *Monitor) isReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ctx.Err() == nil && m.ready && m.now().Before(m.deadline)
}
func (m *Monitor) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ready" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ready := m.isReady()
	if ready && m.check != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		ready = m.check(ctx) == nil
		cancel()
	}
	ready = ready && m.isReady()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	status := "ready"
	if !ready {
		status = "not_ready"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(response{Component: m.component, Status: status})
}

type response struct {
	Component string `json:"component"`
	Status    string `json:"status"`
}

func (m *Monitor) Address() string {
	if m.listener == nil {
		return ""
	}
	return m.listener.Addr().String()
}
func (m *Monitor) Close() {
	if m.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := m.server.Shutdown(ctx); err != nil {
			_ = m.server.Close()
		}
		<-m.done
	}
}

// Probe needs neither a bot token nor database credentials. Identity validation
// prevents accidentally probing the other process on a shared host.
func Probe(ctx context.Context, addr, component string) error {
	if err := ValidateAddress(addr); err != nil {
		return err
	}
	if addr == "" {
		return errors.New("HEALTH_ADDR is required for process readiness")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ready", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	r, err := client.Do(req)
	if err != nil {
		return errors.New("readiness endpoint unavailable")
	}
	defer r.Body.Close()
	var result response
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&result); err != nil || r.StatusCode != http.StatusOK || result.Component != component || result.Status != "ready" {
		return fmt.Errorf("%s is not ready", component)
	}
	return nil
}
