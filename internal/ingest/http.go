package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type doer interface {
	Do(*http.Request) (*http.Response, error)
}

func newClient(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }

func getBytes(ctx context.Context, client doer, address, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", "tucano-indices/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if limit <= 0 {
		limit = 16 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := string(body)
		if len(message) > 300 {
			message = message[:300]
		}
		return nil, fmt.Errorf("GET %s returned HTTP %d: %s", address, resp.StatusCode, message)
	}
	return body, nil
}
