package getter

import (
	"fmt"
	"io"
	"strings"
)

var _ Getter = (*tgzGetter)(nil)

type tgzGetter struct{}

func (g *tgzGetter) Get(opts GetOptions) (io.ReadCloser, string, error) {
	if !isTGZ(opts.URI) {
		return nil, "", fmt.Errorf("uri '%s' is not a valid .tgz ref", opts.URI)
	}

	dat, err := fetchStream(opts)
	if err != nil {
		return nil, "", err
	}

	return dat, opts.URI, nil
}

func isTGZ(url string) bool {
	return strings.HasSuffix(url, ".tgz") || strings.HasSuffix(url, ".tar.gz")
}
