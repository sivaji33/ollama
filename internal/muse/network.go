package muse

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Network struct {
	Client *http.Client
	Guard  *PathGuard
}

func (n *Network) client() *http.Client {
	if n.Client != nil {
		return n.Client
	}
	return &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return validatePublicURL(req.URL)
		},
	}
}

func validatePublicURL(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("absolute http(s) URL required")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return errors.New("localhost fetch is blocked")
	}
	ips, err := net.LookupIP(host)
	if err == nil {
		for _, ip := range ips {
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() {
				return errors.New("private/local network fetch is blocked")
			}
		}
	}
	return nil
}

func (n *Network) Fetch(ctx context.Context, raw string, maxBytes int64) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if err := validatePublicURL(u); err != nil {
		return "", err
	}
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	resp, err := n.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(b)) > maxBytes {
		return "", errors.New("response exceeds configured limit")
	}
	return string(b), nil
}

func (n *Network) Download(ctx context.Context, raw, destination string, maxBytes int64) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if err := validatePublicURL(u); err != nil {
		return "", err
	}
	dst, err := n.Guard.Resolve(destination)
	if err != nil {
		return "", err
	}
	if maxBytes <= 0 {
		maxBytes = 512 * 1024 * 1024
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	resp, err := n.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New(resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".muse-download"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	written, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxBytes+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	if written > maxBytes {
		_ = os.Remove(tmp)
		return "", errors.New("download exceeds configured limit")
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dst, nil
}
