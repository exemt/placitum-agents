package s3info

import (
	"fmt"
	"net/url"
	"strings"
)

type Endpoint struct {
	Host   string
	Secure bool
}

func ParseEndpoint(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Endpoint{}, fmt.Errorf("empty")
	}
	if !strings.Contains(raw, "://") {
		return Endpoint{Host: strings.TrimSuffix(raw, "/"), Secure: false}, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, err
	}
	if u.Host == "" {
		return Endpoint{}, fmt.Errorf("no host")
	}
	return Endpoint{Host: u.Host, Secure: u.Scheme == "https"}, nil
}
