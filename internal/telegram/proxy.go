package telegram

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTP and HTTPS CONNECT and SOCKS5 use Go's own transport. SOCKS4a adds
// remote DNS support for the legacy SOCKS4 proxy option.
func HTTPClient(proxy string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // The Telegram proxy is explicit; never inherit process-wide settings.
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	if strings.TrimSpace(proxy) != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Hostname() == "" {
			return nil, errors.New("invalid Telegram proxy URL")
		}
		switch u.Scheme {
		case "http", "https", "socks5":
			transport.Proxy = http.ProxyURL(u)
		case "socks4":
			if u.User != nil {
				if password, ok := u.User.Password(); ok && password != "" {
					return nil, errors.New("SOCKS4 does not support password authentication")
				}
			}
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialSOCKS4(ctx, address, u)
			}
		default:
			return nil, errors.New("unsupported Telegram proxy scheme")
		}
	}
	return &http.Client{Transport: transport, Timeout: 45 * time.Second}, nil
}
func dialSOCKS4(ctx context.Context, address string, proxy *url.URL) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid SOCKS4 target")
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return nil, errors.New("invalid SOCKS4 target port")
	}
	user := ""
	if proxy.User != nil {
		user = proxy.User.Username()
	}
	if strings.ContainsRune(user, 0) || strings.ContainsRune(host, 0) || len(user) > 255 || len(host) > 255 {
		return nil, errors.New("invalid SOCKS4 target or username")
	}
	proxyHost := proxy.Host
	if proxy.Port() == "" {
		proxyHost = net.JoinHostPort(proxy.Hostname(), "1080")
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", proxyHost)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(15 * time.Second)
	if at, exists := ctx.Deadline(); exists && at.Before(deadline) {
		deadline = at
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	packet := []byte{4, 1, 0, 0, 0, 0, 0, 1}
	binary.BigEndian.PutUint16(packet[2:4], uint16(port))
	ip := net.ParseIP(host)
	remoteDNS := true
	if ip != nil {
		ipv4 := ip.To4()
		if ipv4 == nil {
			return nil, errors.New("SOCKS4 does not support IPv6 literals")
		}
		copy(packet[4:8], ipv4)
		remoteDNS = false
	}
	packet = append(packet, []byte(user)...)
	packet = append(packet, 0)
	if remoteDNS {
		packet = append(packet, []byte(host)...)
		packet = append(packet, 0)
	}
	if _, err := io.Copy(conn, bytes.NewReader(packet)); err != nil {
		return nil, err
	}
	var reply [8]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return nil, err
	}
	if reply[0] != 0 || reply[1] != 90 {
		return nil, errors.New("SOCKS4 proxy rejected connection")
	}
	if !stop() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	ok = true
	return conn, nil
}
