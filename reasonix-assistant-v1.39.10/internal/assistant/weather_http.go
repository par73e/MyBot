package assistant

import (
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type weatherCache struct {
	FetchedAt int64           `json:"fetched_at"`
	Data      json.RawMessage `json:"data"`
}
type weatherPart struct {
	Status    string          `json:"status"`
	FetchedAt string          `json:"fetched_at,omitempty"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func (s WeatherService) token(c weatherCredentials) (string, error) {
	raw, err := os.ReadFile(c.PrivateFile)
	if err != nil {
		return "", errors.New("weather signing key unavailable")
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", errors.New("invalid weather signing key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", errors.New("invalid weather signing key")
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return "", errors.New("weather requires Ed25519 key")
	}
	header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "kid": c.KeyID})
	payload, _ := json.Marshal(map[string]any{"iss": c.Developer, "sub": c.Project, "iat": s.now().Unix() - 30, "exp": s.now().Unix() + 900})
	enc := base64.RawURLEncoding.EncodeToString
	message := enc(header) + "." + enc(payload)
	return message + "." + enc(ed25519.Sign(private, []byte(message))), nil
}
func (s WeatherService) httpFetch(ctx context.Context, path string) (json.RawMessage, error) {
	c, err := s.credentials()
	if err != nil {
		return nil, err
	}
	token, err := s.token(c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+c.Host+path, nil)
	if err != nil {
		return nil, errors.New("invalid weather request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept-Encoding", "gzip")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("weather connection failed or timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather API HTTP %d", resp.StatusCode)
	}
	reader := io.Reader(io.LimitReader(resp.Body, 2<<20))
	if resp.Header.Get("Content-Encoding") == "gzip" {
		z, e := gzip.NewReader(reader)
		if e != nil {
			return nil, errors.New("invalid weather compression")
		}
		defer z.Close()
		reader = z
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 || !json.Valid(raw) {
		return nil, errors.New("invalid or oversized weather response")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return nil, errors.New("invalid weather response object")
	}
	if code, ok := envelope["code"]; ok && string(code) != `"200"` {
		return nil, errors.New("weather API returned unsuccessful data")
	}
	if _, ok := envelope["error"]; ok {
		return nil, errors.New("weather API returned error data")
	}
	return raw, nil
}
func (s WeatherService) part(ctx context.Context, path string, ttl time.Duration) weatherPart {
	repo := NewRepository(s.Home)
	cfg, _ := s.Config()
	id := mailHash(cfg.CredentialFile + "\x00" + path)
	var cached weatherCache
	_ = repo.transact(ctx, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT payload FROM assistant_entities WHERE kind='weather_cache' AND id=?", id).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal([]byte(raw), &cached)
	})
	age := s.now().Sub(time.Unix(cached.FetchedAt, 0))
	stamp := func(sec int64) string { return time.Unix(sec, 0).UTC().Format(time.RFC3339) }
	if len(cached.Data) > 0 && age >= 0 && age < ttl {
		return weatherPart{Status: "cached", FetchedAt: stamp(cached.FetchedAt), Data: cached.Data}
	}
	fetch := s.Fetch
	if fetch == nil {
		fetch = s.httpFetch
	}
	raw, err := fetch(ctx, path)
	if err == nil {
		record := weatherCache{s.now().Unix(), raw}
		saveErr := repo.transact(ctx, func(tx *sql.Tx) error { return saveEntity(ctx, tx, "weather_cache", id, record) })
		out := weatherPart{Status: "fresh", FetchedAt: stamp(record.FetchedAt), Data: raw}
		if saveErr != nil {
			out.Error = "cache persistence failed; live data available"
		}
		return out
	}
	if len(cached.Data) > 0 && age >= 0 && age <= 2*time.Hour {
		return weatherPart{Status: "stale", FetchedAt: stamp(cached.FetchedAt), Data: cached.Data, Error: "refresh failed; old data for reference only"}
	}
	return weatherPart{Status: "unavailable", Error: err.Error()}
}
