package assistant

import (
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWeatherNativeJWTTransportGzipAndErrors(t *testing.T) {
	s, _ := weatherFixture(t)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyfile := filepath.Join(s.Home, "fixture-private.pem")
	if err = os.WriteFile(keyfile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	writeWeatherFixture(t, filepath.Join(s.Home, "credential.json"), weatherCredentials{Host: "fixture.re.qweatherapi.com", Developer: "DEVELOPER", Project: "PROJECT", KeyID: "KEY", PrivateFile: keyfile})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		pieces := strings.Split(token, ".")
		if len(pieces) != 3 {
			t.Error("missing JWT")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sig, e := base64.RawURLEncoding.DecodeString(pieces[2])
		if e != nil || !ed25519.Verify(public, []byte(pieces[0]+"."+pieces[1]), sig) {
			t.Error("bad signature")
		}
		payload, _ := base64.RawURLEncoding.DecodeString(pieces[1])
		var claims map[string]any
		if json.Unmarshal(payload, &claims) != nil || claims["iss"] != "DEVELOPER" || claims["sub"] != "PROJECT" || claims["exp"] != float64(s.now().Unix()+900) {
			t.Error("bad claims")
		}
		switch r.URL.Path {
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			z := gzip.NewWriter(w)
			_, _ = z.Write([]byte(`{"temperature":{"value":20}}`))
			_ = z.Close()
		case "/redirect":
			http.Redirect(w, r, "https://evil.invalid", http.StatusFound)
		case "/error":
			_, _ = w.Write([]byte(`{"error":{"detail":"SECRET"}}`))
		case "/code":
			_, _ = w.Write([]byte(`{"code":"403"}`))
		case "/array":
			_, _ = w.Write([]byte(`[]`))
		case "/huge":
			_, _ = w.Write([]byte(`{"data":"` + strings.Repeat("x", 2<<20) + `"}`))
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("SECRET"))
		}
	}))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "example.com"
	address := strings.TrimPrefix(server.URL, "https://")
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous; transport.CloseIdleConnections() })
	raw, err := s.httpFetch(t.Context(), "/gzip")
	if err != nil || !strings.Contains(string(raw), "temperature") {
		t.Fatal(string(raw), err)
	}
	for _, path := range []string{"/redirect", "/error", "/code", "/array", "/huge", "/forbidden"} {
		if _, err = s.httpFetch(t.Context(), path); err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("accepted invalid response or leaked provider body", path, err)
		}
	}
}
