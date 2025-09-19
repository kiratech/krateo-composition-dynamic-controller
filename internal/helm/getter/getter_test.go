package getter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGet(t *testing.T) {
	tests := []struct {
		name    string
		opts    GetOptions
		wantErr bool
	}{
		{
			name: "OCI URI",
			opts: GetOptions{
				URI: "oci://registry-1.docker.io/bitnamicharts/nginx",
			},
			wantErr: false,
		},
		{
			name: "TGZ URI",
			opts: GetOptions{
				URI: "https://raw.githubusercontent.com/krateoplatformops/helm-charts/refs/heads/gh-pages/api-1.0.0.tgz",
			},
			wantErr: false,
		},
		{
			name: "HTTP URI",
			opts: GetOptions{
				URI:     "https://charts.krateo.io",
				Repo:    "fireworks-app",
				Version: "1.1.10",
			},
			wantErr: false,
		},
		{
			name: "Invalid URI",
			opts: GetOptions{
				URI: "invalid://example.com/chart",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Delete tmp folder after each test
			cacheDir := func() string {
				if v := os.Getenv("HELM_CHART_CACHE_DIR"); v != "" {
					return v
				}
				return "/tmp/helmchart-cache"
			}()
			_, _, err := Get(tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("Get() error = %v, wantErr %v", err, tt.wantErr)
			}
			os.RemoveAll(cacheDir)
		})
	}
}

func TestTGZGetter(t *testing.T) {
	opts := GetOptions{
		URI: "https://raw.githubusercontent.com/krateoplatformops/helm-charts/refs/heads/gh-pages/api-1.0.0.tgz",
	}

	g := &tgzGetter{}
	_, _, err := g.Get(opts)
	assert.NoError(t, err)
}

func TestOCIGetter(t *testing.T) {
	opts := GetOptions{
		URI: "oci://registry-1.docker.io/bitnamicharts/nginx",
	}

	g, err := newOCIGetter()
	assert.NoError(t, err)

	_, _, err = g.Get(opts)
	assert.NoError(t, err)
}

func TestRepoGetter(t *testing.T) {
	opts := GetOptions{
		URI:     "https://charts.krateo.io",
		Repo:    "fireworks-app",
		Version: "1.1.10",
	}

	g := &repoGetter{}
	_, _, err := g.Get(opts)
	assert.NoError(t, err)
}

func TestFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test data"))
	}))
	defer server.Close()

	opts := GetOptions{
		URI: server.URL,
	}

	data, err := fetchStream(opts)
	assert.NoError(t, err)
	b, err := io.ReadAll(data)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, "test data", string(b))
}

// ...existing code...

// Test that Get uses the disk cache and does not re-download when the cache file exists
func TestGet_CacheAvoidsRedownload(t *testing.T) {
	// create a temp dir for cache and force the library to use it
	tmpdir := t.TempDir()
	if err := os.Setenv("HELM_CHART_CACHE_DIR", tmpdir); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
	defer os.Unsetenv("HELM_CHART_CACHE_DIR")

	var mu sync.Mutex
	requests := 0

	// server returns some data and counts requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("chart-data"))
	}))
	defer server.Close()

	uri := server.URL + "/test.tgz"
	opts := GetOptions{URI: uri}

	// First call should hit the server and populate cache
	_, _, err := Get(opts)
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}

	// check server was called exactly once
	mu.Lock()
	if requests != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 request after first Get, got %d", requests)
	}
	mu.Unlock()

	// compute expected cache file path (same logic as Get())
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", opts.URI, opts.Version, opts.Repo)))
	cacheFile := filepath.Join(tmpdir, hex.EncodeToString(h[:])+".tgz")
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("expected cache file at %s, stat error: %v", cacheFile, err)
	}

	// Second call should read from cache and NOT call server again
	_, _, err = Get(opts)
	if err != nil {
		t.Fatalf("second Get failed: %v", err)
	}

	mu.Lock()
	if requests != 1 {
		mu.Unlock()
		t.Fatalf("expected cached access, server should have been called once, got %d", requests)
	}
	mu.Unlock()
}
