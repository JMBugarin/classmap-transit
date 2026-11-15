package transit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxFeedBytes = 8 << 20

func Fetch(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/x-protobuf, application/x-google-protobuf")
	req.Header.Set("User-Agent", "classmap-transit/0.1")
	res, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream HTTP %d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, maxFeedBytes+1))
	if e != nil {
		return nil, e
	}
	if len(b) > maxFeedBytes {
		return nil, errors.New("feed too large")
	}
	return b, nil
}

func Poll(ctx context.Context, s *Store, c *http.Client, url, schedule string, every time.Duration, logf func(string, ...any)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if b, e := Fetch(ctx, c, url); e != nil {
			logf("poll: %v", e)
		} else if _, e = s.Accept(ctx, "live", "trips", schedule, b, s.Now()); e != nil {
			logf("poll accept: %v", e)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
