package hongguo

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// A VPN's Fake-IP DNS (198.18.0.0/15) can route the official site through
// an edge that takes 20 seconds a page. Like hongguo-drama, resolve the
// site's own host through fixed DNS-over-HTTPS resolvers and connect to the
// public address they give, keeping TLS to the real host name.
const siteHost = "hongguoduanju.com"

var (
	dnsMu      sync.Mutex
	dnsAddrs   []netip.Addr
	dnsExpires time.Time
)

var dohClient = &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{Proxy: nil}}

func publicIPv4(a netip.Addr) bool {
	if !a.Is4() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return false
		}
	}
	return true
}

func resolveTrusted(ctx context.Context) ([]netip.Addr, error) {
	dnsMu.Lock()
	if time.Now().Before(dnsExpires) {
		addrs := dnsAddrs
		dnsMu.Unlock()
		return addrs, nil
	}
	dnsMu.Unlock()

	type result struct {
		addrs []netip.Addr
		err   error
	}
	endpoints := []string{"https://223.5.5.5/resolve", "https://1.1.1.1/dns-query"}
	ch := make(chan result, len(endpoints))
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for _, ep := range endpoints {
		go func() {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ep+"?name="+siteHost+"&type=A", nil)
			req.Header.Set("Accept", "application/dns-json")
			res, err := dohClient.Do(req)
			if err != nil {
				ch <- result{err: err}
				return
			}
			defer res.Body.Close()
			var body struct {
				Status int
				Answer []struct {
					Type int
					Data string
				}
			}
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Status != 0 {
				ch <- result{err: errors.New("DoH 响应无效")}
				return
			}
			var addrs []netip.Addr
			for _, a := range body.Answer {
				if a.Type != 1 {
					continue
				}
				ip, err := netip.ParseAddr(strings.TrimSpace(a.Data))
				if err != nil || !publicIPv4(ip) {
					ch <- result{err: errors.New("DoH 返回了非公网地址")}
					return
				}
				addrs = append(addrs, ip)
			}
			if len(addrs) == 0 {
				ch <- result{err: errors.New("DoH 无结果")}
				return
			}
			ch <- result{addrs: addrs}
		}()
	}
	var lastErr error
	for range endpoints {
		r := <-ch
		if r.err == nil {
			dnsMu.Lock()
			dnsAddrs, dnsExpires = r.addrs, time.Now().Add(5*time.Minute)
			dnsMu.Unlock()
			return r.addrs, nil
		}
		lastErr = r.err
	}
	return nil, lastErr
}

var plainDialer = &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}

// siteDial connects to the official site through the trusted addresses,
// and anything else, or the site when DoH fails, as usual.
func siteDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err == nil && host == siteHost {
		if addrs, err := resolveTrusted(ctx); err == nil {
			for _, ip := range addrs {
				d := &net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}
				if conn, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port)); err == nil {
					return conn, nil
				}
			}
		}
	}
	return plainDialer.DialContext(ctx, network, addr)
}

var siteClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		DialContext:         siteDial,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 8 * time.Second,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	},
}
