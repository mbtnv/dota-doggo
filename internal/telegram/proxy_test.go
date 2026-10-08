package telegram

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHTTPAndHTTPSProxy(t *testing.T) {
	for _, tls := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%t", tls), func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Host != "example.invalid" || r.URL.Path != "/bot123:test/getMe" {
					t.Errorf("proxy request %s", r.URL)
				}
				reply(w, User{ID: 1, Username: "proxy_bot"})
			})
			s := httptest.NewUnstartedServer(h)
			if tls {
				s.StartTLS()
			} else {
				s.Start()
			}
			defer s.Close()
			hc, err := HTTPClient(s.URL)
			if err != nil {
				t.Fatal(err)
			}
			if tls {
				hc.Transport.(*http.Transport).TLSClientConfig = s.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			}
			c, err := New(Options{Token: "123:test", BaseURL: "http://example.invalid", HTTPClient: hc})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			me, err := c.Me(context.Background())
			if err != nil || me.Username != "proxy_bot" {
				t.Fatal(me, err)
			}
		})
	}
}
func TestSOCKSProxyRemoteDNS(t *testing.T) {
	for _, scheme := range []string{"socks4", "socks5"} {
		t.Run(scheme, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			finished := make(chan error, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					finished <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(conn)
				var host string
				var port uint16
				if scheme == "socks4" {
					var packet [8]byte
					if _, err := io.ReadFull(r, packet[:]); err != nil {
						finished <- err
						return
					}
					if packet[0] != 4 || packet[1] != 1 || packet[7] != 1 {
						finished <- fmt.Errorf("invalid SOCKS4 handshake %v", packet)
						return
					}
					port = binary.BigEndian.Uint16(packet[2:4])
					if _, err := r.ReadString(0); err != nil {
						finished <- err
						return
					}
					host, err = r.ReadString(0)
					host = strings.TrimSuffix(host, "\x00")
					if err == nil {
						_, err = conn.Write([]byte{0, 90, 0, 0, 0, 0, 0, 0})
					}
				} else {
					var hello [2]byte
					if _, err := io.ReadFull(r, hello[:]); err != nil {
						finished <- err
						return
					}
					methods := make([]byte, int(hello[1]))
					_, err = io.ReadFull(r, methods)
					if err != nil {
						finished <- err
						return
					}
					_, _ = conn.Write([]byte{5, 0})
					var request [5]byte
					if _, err := io.ReadFull(r, request[:]); err != nil {
						finished <- err
						return
					}
					if request[0] != 5 || request[1] != 1 || request[3] != 3 {
						finished <- fmt.Errorf("invalid SOCKS5 handshake %v", request)
						return
					}
					name := make([]byte, int(request[4]))
					_, err = io.ReadFull(r, name)
					host = string(name)
					var b [2]byte
					if err == nil {
						_, err = io.ReadFull(r, b[:])
						port = binary.BigEndian.Uint16(b[:])
					}
					if err == nil {
						_, err = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
					}
				}
				if err != nil {
					finished <- err
					return
				}
				if host != "example.invalid" || port != 80 {
					finished <- fmt.Errorf("destination %s:%d", host, port)
					return
				}
				req, err := http.ReadRequest(r)
				if err != nil {
					finished <- err
					return
				}
				_ = req.Body.Close()
				body := `{"ok":true,"result":{"id":1,"username":"proxy_bot"}}`
				_, err = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
				finished <- err
			}()
			c, err := New(Options{Token: "123:test", BaseURL: "http://example.invalid", ProxyURL: scheme + "://" + ln.Addr().String(), MaxAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			me, callErr := c.Me(ctx)
			serverErr := <-finished
			if callErr != nil || serverErr != nil || me.Username != "proxy_bot" {
				t.Fatal(me, callErr, serverErr)
			}
		})
	}
}
func TestInvalidProxyAndExplicitTransport(t *testing.T) {
	for _, proxy := range []string{"bad", "ftp://localhost", "socks4://user:password@localhost"} {
		if _, err := HTTPClient(proxy); err == nil {
			t.Fatalf("accepted %s", proxy)
		}
	}
	c, err := HTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if c.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("inherited global proxy")
	}
}

func TestHTTPSBotThroughHTTPAndHTTPSCONNECT(t *testing.T) {
	for _, secureProxy := range []bool{false, true} {
		t.Run(fmt.Sprintf("secure_proxy=%t", secureProxy), func(t *testing.T) {
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/bot123:test/getMe" {
					t.Error(r.URL.Path)
				}
				reply(w, User{ID: 1, Username: "tunnel_bot"})
			}))
			defer target.Close()
			u, _ := url.Parse(target.URL)
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodConnect || r.Host != u.Host || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
					t.Errorf("CONNECT %s %s auth=%q", r.Method, r.Host, r.Header.Get("Proxy-Authorization"))
					http.Error(w, "invalid tunnel", 400)
					return
				}
				upstream, err := net.DialTimeout("tcp", u.Host, time.Second)
				if err != nil {
					t.Error(err)
					http.Error(w, "unavailable", 502)
					return
				}
				defer upstream.Close()
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
				copied := make(chan struct{})
				go func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close(); close(copied) }()
				_, _ = io.Copy(conn, upstream)
				_ = conn.Close()
				<-copied
			}))
			if secureProxy {
				proxy.StartTLS()
			} else {
				proxy.Start()
			}
			defer proxy.Close()
			pu, _ := url.Parse(proxy.URL)
			pu.User = url.UserPassword("user", "pass")
			hc, err := HTTPClient(pu.String())
			if err != nil {
				t.Fatal(err)
			}
			hc.Transport.(*http.Transport).TLSClientConfig = target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			c, err := New(Options{Token: "123:test", BaseURL: target.URL, HTTPClient: hc, MaxAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			me, err := c.Me(ctx)
			if err != nil || me.Username != "tunnel_bot" {
				t.Fatal(me, err)
			}
		})
	}
}
