package assistant

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

func publicNewsAddress(ctx context.Context, address string) (net.IP, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, v := range ips {
		if !v.IP.IsGlobalUnicast() || v.IP.IsPrivate() || v.IP.IsLoopback() || v.IP.IsLinkLocalUnicast() {
			return nil, errors.New("news destination is not public")
		}
	}
	for _, v := range ips {
		if v.IP.To4() != nil {
			return v.IP, nil
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("news DNS returned no addresses")
	}
	return ips[0].IP, nil
}
func newsPublicURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Port() != "" && u.Port() != "443" {
		return errors.New("news requires a public HTTPS URL")
	}
	return nil
}
func (s HotNewsService) fetchPublic(ctx context.Context, raw string) ([]byte, error) {
	if err := newsPublicURL(raw); err != nil {
		return nil, err
	}
	if s.Fetch != nil {
		return s.Fetch(ctx, raw)
	}
	tr := &http.Transport{TLSHandshakeTimeout: 6 * time.Second, ResponseHeaderTimeout: 7 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		ip, e := publicNewsAddress(ctx, address)
		if e != nil {
			return nil, e
		}
		_, port, _ := net.SplitHostPort(address)
		return (&net.Dialer{Timeout: 6 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, Timeout: 9 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 4 {
			return errors.New("too many news redirects")
		}
		return newsPublicURL(r.URL.String())
	}}
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, e
	}
	request.Header.Set("User-Agent", "Friday-AI-News/1.0")
	resp, e := client.Do(request)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("news HTTP %d", resp.StatusCode)
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if len(body) > 4*1024*1024 {
		return nil, errors.New("news body exceeds 4 MiB")
	}
	return body, e
}
