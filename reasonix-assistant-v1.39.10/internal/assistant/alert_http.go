package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const alertNMC = "https://www.nmc.cn"
const alertShanghai = "https://d1.weather.com.cn/shweather/alarm.json"
const alertProduct = "https://product.weather.com.cn"

// No cookies, credentials, arbitrary URLs or redirects. A 304 is a successful
// validation of a stored body, never an empty warning list by itself.
func (s AlertService) fetch(ctx context.Context, raw, ref string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Scheme != "https" || !slicesAlertHost(u.Host) {
		return nil, errors.New("unsupported official alert origin")
	}
	cache, err := alertRead[alertCache](ctx, s.repo(), "alert_http", mailHash(raw))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if s.now().Before(cache.BlockedUntil) {
		return nil, errors.New("official alert endpoint is backing off")
	}
	requestURL := raw
	if s.testOrigin != "" {
		requestURL = s.testOrigin + "/" + u.Host + u.RequestURI()
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Friday-Official-Alerts/1.0")
	req.Header.Set("Referer", ref)
	if cache.ETag != "" {
		req.Header.Set("If-None-Match", cache.ETag)
	} else if cache.Modified != "" {
		req.Header.Set("If-Modified-Since", cache.Modified)
	}
	client := http.Client{Transport: http.DefaultTransport}
	if s.Client != nil {
		client = *s.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("official alert redirects are not followed")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("official alert connection failed or timed out")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		if len(cache.Body) == 0 {
			return nil, errors.New("official alert 304 has no stored body")
		}
		if err = alertResponseFresh(resp, s.now()); err != nil {
			return nil, err
		}
		cache.CheckedAt = s.now()
		return cache.Body, s.saveCache(ctx, raw, cache)
	}
	if resp.StatusCode != http.StatusOK {
		wait := time.Duration(0)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			wait = 5 * time.Minute
		}
		if v := resp.Header.Get("Retry-After"); v != "" {
			if n, e := strconv.Atoi(v); e == nil && n > 0 {
				wait = max(wait, time.Duration(n)*time.Second)
			} else if at, e := http.ParseTime(v); e == nil {
				wait = max(wait, at.Sub(s.now()))
			}
		}
		if wait > 0 {
			cache.BlockedUntil = s.now().Add(wait)
			if e := s.saveCache(ctx, raw, cache); e != nil {
				return nil, e
			}
		}
		return nil, fmt.Errorf("official alert HTTP %d; prior state retained", resp.StatusCode)
	}
	if err = alertResponseFresh(resp, s.now()); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) == 0 || len(body) > 1<<20 {
		return nil, errors.New("official alert response is empty, incomplete or oversized")
	}
	// Parsers validate the protocol again on every hit, including cached bodies.
	// Invalid data can never be mistaken for a successful empty list.
	cache = alertCache{Body: body, ETag: resp.Header.Get("ETag"), Modified: resp.Header.Get("Last-Modified"), CheckedAt: s.now()}
	return body, s.saveCache(ctx, raw, cache)
}
func slicesAlertHost(host string) bool {
	return host == "www.nmc.cn" || host == "d1.weather.com.cn" || host == "product.weather.com.cn"
}
func alertResponseFresh(r *http.Response, now time.Time) error {
	if n, e := strconv.Atoi(r.Header.Get("Age")); e == nil && n > 900 {
		return errors.New("official alert HTTP cache is stale")
	}
	if stamp, e := http.ParseTime(r.Header.Get("Date")); e == nil && (now.Sub(stamp) > 15*time.Minute || stamp.Sub(now) > 5*time.Minute) {
		return errors.New("official alert response timestamp is stale or in the future")
	}
	return nil
}
func (s AlertService) saveCache(ctx context.Context, key string, c alertCache) error {
	return s.repo().transact(ctx, func(tx *sql.Tx) error { return saveEntity(ctx, tx, "alert_http", mailHash(key), c) })
}
func alertJSON(raw []byte, prefix, suffix string, out any) error {
	text := strings.TrimSpace(string(raw))
	text = strings.TrimSuffix(text, ";")
	if prefix != "" {
		if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, suffix) {
			return errors.New("official alert data wrapper changed")
		}
		text = strings.TrimSuffix(strings.TrimPrefix(text, prefix), suffix)
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return errors.New("official alert response is not valid data")
	}
	return nil
}
