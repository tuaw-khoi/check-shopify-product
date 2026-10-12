package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type URLGuard struct {
	AllowPrivate bool
	Resolver     *net.Resolver
}

func (g URLGuard) NormalizeStoreURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("vui lòng nhập URL của Shopify store")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("URL store không hợp lệ")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("chỉ hỗ trợ URL http hoặc https")
	}
	if u.User != nil {
		return nil, errors.New("URL không được chứa username/password")
	}
	if port := u.Port(); !g.AllowPrivate && port != "" && port != "80" && port != "443" {
		return nil, errors.New("chỉ hỗ trợ cổng web 80 và 443")
	}
	if err := g.ValidateHost(context.Background(), u.Hostname()); err != nil {
		return nil, err
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

func (g URLGuard) ValidateURL(ctx context.Context, u *url.URL) error {
	if u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return errors.New("URL đích không hợp lệ")
	}
	if port := u.Port(); !g.AllowPrivate && port != "" && port != "80" && port != "443" {
		return errors.New("redirect tới cổng không được phép")
	}
	return g.ValidateHost(ctx, u.Hostname())
}

func (g URLGuard) ValidateHost(ctx context.Context, hostname string) error {
	hostname = strings.TrimSpace(strings.TrimSuffix(hostname, "."))
	if hostname == "" {
		return errors.New("hostname trống")
	}
	if ip := net.ParseIP(hostname); ip != nil {
		if !g.AllowPrivate && !isPublicIP(ip) {
			return errors.New("URL trỏ tới địa chỉ mạng riêng hoặc nội bộ")
		}
		return nil
	}
	resolver := g.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addresses, err := resolver.LookupIPAddr(lookupCtx, hostname)
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("không phân giải được hostname %s", hostname)
	}
	if !g.AllowPrivate {
		for _, address := range addresses {
			if !isPublicIP(address.IP) {
				return errors.New("URL trỏ tới địa chỉ mạng riêng hoặc nội bộ")
			}
		}
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

func (g URLGuard) NewHTTPClient(timeout time.Duration) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, splitErr
			}
			resolver := g.Resolver
			if resolver == nil {
				resolver = net.DefaultResolver
			}
			addresses, lookupErr := resolver.LookupIPAddr(ctx, host)
			if lookupErr != nil || len(addresses) == 0 {
				return nil, fmt.Errorf("không phân giải được hostname %s", host)
			}
			for _, candidate := range addresses {
				if !g.AllowPrivate && !isPublicIP(candidate.IP) {
					return nil, errors.New("hostname phân giải tới địa chỉ mạng riêng hoặc nội bộ")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          30,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       45 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{
		Timeout:   timeout,
		Jar:       jar,
		Transport: transport,
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 6 {
			return errors.New("quá nhiều redirect")
		}
		if err := g.ValidateURL(req.Context(), req.URL); err != nil {
			return err
		}
		return nil
	}
	return client, nil
}

func sameHostname(a, b *url.URL) bool {
	return a != nil && b != nil && strings.EqualFold(strings.TrimPrefix(a.Hostname(), "www."), strings.TrimPrefix(b.Hostname(), "www."))
}
