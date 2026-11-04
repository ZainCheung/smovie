package hongguo

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A VPN's Fake-IP DNS (198.18.0.0/15) routes hosts by name through its
// proxy: the official site through an edge that takes 20 seconds a page,
// and the video CDN at 1 MB/s where it gives 100 MB/s direct. Like
// hongguo-drama, resolve hosts through fixed DNS-over-HTTPS resolvers and
// connect to the public address they give, keeping TLS to the real host
// name. A host that cannot be reached so goes the usual way for a while.
const siteHost = "hongguoduanju.com"

type dnsEntry struct {
	addrs   []netip.Addr
	expires time.Time
	failed  bool // DoH or the direct connection failed: the system's way
}

var (
	dnsMu    sync.Mutex
	dnsCache = map[string]dnsEntry{}
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

// dohEndpoints, in order of preference: AliDNS answers with the CDN's
// nodes near China, where the media is; Cloudflare when it fails. For the
// site, whichever answers first, as some of the nodes AliDNS gives it
// hang for 20 seconds.
var dohEndpoints = []string{"https://223.5.5.5/resolve", "https://1.1.1.1/dns-query"}

func resolveTrusted(ctx context.Context, host string) ([]netip.Addr, error) {
	dnsMu.Lock()
	if e, ok := dnsCache[host]; ok && time.Now().Before(e.expires) {
		dnsMu.Unlock()
		if e.failed {
			return nil, errors.New("DoH 不可用")
		}
		return e.addrs, nil
	}
	dnsMu.Unlock()

	type result struct {
		addrs []netip.Addr
		err   error
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	// One channel per resolver, read in order of preference; for the site
	// they share one, read as they answer.
	race := host == siteHost
	shared := make(chan result, len(dohEndpoints))
	results := make([]chan result, len(dohEndpoints))
	for i, ep := range dohEndpoints {
		ch := shared
		if !race {
			ch = make(chan result, 1)
		}
		results[i] = ch
		go func() {
			addrs, err := dohLookup(ctx, ep, host)
			ch <- result{addrs, err}
		}()
	}
	var lastErr error
	for _, ch := range results {
		r := <-ch
		if r.err == nil {
			setDNS(host, dnsEntry{addrs: r.addrs, expires: time.Now().Add(5 * time.Minute)})
			return r.addrs, nil
		}
		lastErr = r.err
	}
	markFailed(host)
	return nil, lastErr
}

func setDNS(host string, e dnsEntry) {
	dnsMu.Lock()
	dnsCache[host] = e
	dnsMu.Unlock()
}

func markFailed(host string) {
	setDNS(host, dnsEntry{failed: true, expires: time.Now().Add(5 * time.Minute)})
}

func dohLookup(ctx context.Context, ep, host string) ([]netip.Addr, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ep+"?name="+url.QueryEscape(host)+"&type=A", nil)
	req.Header.Set("Accept", "application/dns-json")
	res, err := dohClient.Do(req)
	if err != nil {
		return nil, err
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
		return nil, errors.New("DoH 响应无效")
	}
	var addrs []netip.Addr
	for _, a := range body.Answer {
		if a.Type != 1 {
			continue // the CNAMEs on the way
		}
		ip, err := netip.ParseAddr(strings.TrimSpace(a.Data))
		if err != nil || !publicIPv4(ip) {
			return nil, errors.New("DoH 返回了非公网地址")
		}
		addrs = append(addrs, ip)
	}
	if len(addrs) == 0 {
		return nil, errors.New("DoH 无结果")
	}
	return addrs, nil
}

var plainDialer = &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}

// directDial connects to a host through the addresses DoH gives, and to
// an address, or a host DoH or those addresses fail, as usual.
func directDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err == nil && net.ParseIP(host) == nil && host != "localhost" {
		if addrs, err := resolveTrusted(ctx, host); err == nil {
			for _, ip := range addrs[:min(len(addrs), 2)] {
				d := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
				if conn, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port)); err == nil {
					return conn, nil
				}
			}
			if ctx.Err() == nil {
				markFailed(host)
			}
		}
	}
	return plainDialer.DialContext(ctx, network, addr)
}

func directTransport() *http.Transport {
	return &http.Transport{
		DialContext:         directDial,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 8 * time.Second,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
}

var (
	siteClient = &http.Client{Timeout: 20 * time.Second, Transport: directTransport()}
	// mediaClient downloads episodes: no overall timeout, as media can be large.
	mediaClient = &http.Client{Transport: directTransport()}
)
