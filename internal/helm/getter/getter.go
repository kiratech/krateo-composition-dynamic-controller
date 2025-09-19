package getter

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/krateoplatformops/unstructured-runtime/pkg/logging"
)

type GetOptions struct {
	Logging               logging.Logger
	URI                   string
	Version               string
	Repo                  string
	InsecureSkipVerifyTLS bool
	Username              string
	Password              string
	PassCredentialsAll    bool
}

// Getter is an interface to support GET to the specified URI.
type Getter interface {
	// Get file content by url string
	Get(opts GetOptions) (io.ReadCloser, string, error)
}

func Get(opts GetOptions) (io.ReadCloser, string, error) {
	// Simple disk cache: env HELM_CHART_CACHE_DIR or /tmp/helmchart-cache
	cacheDir := func() string {
		if v := os.Getenv("HELM_CHART_CACHE_DIR"); v != "" {
			return v
		}
		return "/tmp/helmchart-cache"
	}()

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		// non-blocking: log and continue to fetch from network
	}

	// cache key = sha256(uri|version|repo)
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", opts.URI, opts.Version, opts.Repo)))
	cacheFile := filepath.Join(cacheDir, hex.EncodeToString(h[:])+".tgz")

	// if cached file exists, open and return it (caller must Close)
	if fi, err := os.Stat(cacheFile); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
		f, err := os.Open(cacheFile)
		if err == nil {
			return f, cacheFile, nil
		}
		// if error opening, fallthrough to refetch
	}

	// fallback: call the appropriate getter and stream to cache file
	var (
		rc  io.ReadCloser
		uri string
		err error
	)

	// delegate to specific getters
	if isOCI(opts.URI) {
		g, errNew := newOCIGetter()
		if errNew != nil {
			return nil, "", errNew
		}
		rc, uri, err = g.Get(opts)
	} else if isTGZ(opts.URI) {
		g := &tgzGetter{}
		rc, uri, err = g.Get(opts)
	} else if isHTTP(opts.URI) {
		g := &repoGetter{}
		rc, uri, err = g.Get(opts)
	} else {
		return nil, "", fmt.Errorf("no handler found for url: %s", opts.URI)
	}
	if err != nil {
		return nil, "", err
	}
	// ensure rc is closed on error / after copy
	defer func() {
		// if we return success we'll re-open cached file and return that handle instead
	}()

	// write stream -> tmp file in cache dir
	tmpf, err := os.CreateTemp(cacheDir, "chart-*.tmp")
	if err != nil {
		rc.Close()
		return nil, "", err
	}
	_, err = io.Copy(tmpf, rc)
	// free original stream
	_ = rc.Close()
	// close tmp
	if cerr := tmpf.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmpf.Name())
		return nil, "", err
	}

	// atomic move to final cache path
	if err := os.Rename(tmpf.Name(), cacheFile); err != nil {
		// if rename fails, try to remove tmp and return file directly
		os.Remove(tmpf.Name())
		return nil, "", err
	}

	// open cached file for reading and return it
	f, err := os.Open(cacheFile)
	if err != nil {
		return nil, "", err
	}
	return f, uri, nil
}

func fetchStream(opts GetOptions) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, opts.URI, nil)
	if err != nil {
		return nil, err
	}
	if opts.PassCredentialsAll {
		if opts.Username != "" && opts.Password != "" {
			req.SetBasicAuth(opts.Username, opts.Password)
		}
	}
	resp, err := newHTTPClient(opts).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("failed to fetch %s: %s", opts.URI, resp.Status)
	}
	// return the body stream directly; caller is responsible for closing it
	return resp.Body, nil
}

func newHTTPClient(opts GetOptions) *http.Client {
	transport := &http.Transport{
		DisableCompression: true,
		Proxy:              http.ProxyFromEnvironment,
	}

	if opts.InsecureSkipVerifyTLS {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
		} else {
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   1 * time.Minute,
		// CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// 	fmt.Printf("redir: %v\n", via)
		// 	return nil
		// },
	}
}
