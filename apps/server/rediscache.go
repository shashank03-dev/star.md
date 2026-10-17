package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// The caller's key already contains repo@HEAD#paramsVersion. Keep the
	// namespace stable across server instances and separate from other Redis
	// consumers (and from future cache formats).
	redisContractKeyPrefix = "stellar:contract:v1:"
	redisMaxKeyBytes       = 512
	redisMaxBlobBytes      = 4 << 20 // contracts are normally much smaller
	redisMaxRequestBytes   = 9 << 20 // below Upstash's 10 MB REST limit
	redisMaxResponseBytes  = 8 << 20 // bound allocation even on corrupt replies
)

// redisCache shares contracts across instances using Upstash's Redis REST API.
// The cache is best-effort: HTTP failures and malformed data are misses, and
// writes fail silently because the Cache interface does not return errors.
type redisCache struct {
	endpoint string
	token    string
	client   *http.Client
}

var _ Cache = (*redisCache)(nil)

func newRedisCache(endpoint, token string) (Cache, error) {
	return newRedisCacheWithClient(endpoint, token, nil)
}

// The client parameter lets TLS httptest servers exercise real HTTP requests.
// A copy prevents the cache from modifying a caller's client configuration.
func newRedisCacheWithClient(endpoint, token string, base *http.Client) (Cache, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		// Never include the supplied URL in errors; it might embed credentials.
		return nil, errors.New("redis cache: invalid HTTPS REST endpoint")
	}
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("redis cache: missing or invalid REST token")
	}
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	if client.Timeout == 0 || client.Timeout > cacheOpTimeout {
		client.Timeout = cacheOpTimeout
	}
	// Never forward the bearer token across redirects, even to the same host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &redisCache{endpoint: u.String(), token: token, client: &client}, nil
}

func redisKeyOK(key string) bool {
	if key == "" || len(key) > redisMaxKeyBytes || !utf8.ValidString(key) || strings.TrimSpace(key) != key {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] == 0x7f {
			return false
		}
	}
	return true
}

func redisContractJSONOK(blob []byte) bool {
	if len(blob) == 0 || len(blob) > redisMaxBlobBytes || !json.Valid(blob) {
		return false
	}
	// A contract is an object. Valid JSON scalars, arrays, and null are corrupt
	// cache entries, not valid contracts.
	return bytes.HasPrefix(bytes.TrimSpace(blob), []byte{'{'})
}

func (c *redisCache) Get(key string) ([]byte, bool) {
	if !redisKeyOK(key) {
		return nil, false
	}
	result, ok := c.command("GET", redisContractKeyPrefix+key)
	if !ok {
		return nil, false
	}
	var value string
	if err := json.Unmarshal(result, &value); err != nil {
		return nil, false // missing key, wrong type, or corrupt Redis reply
	}
	blob := []byte(value)
	if !redisContractJSONOK(blob) {
		return nil, false
	}
	return blob, true
}

func (c *redisCache) Put(key string, blob []byte, ttl time.Duration) {
	if !redisKeyOK(key) || !redisContractJSONOK(blob) || ttl <= 0 {
		return
	}
	// Redis EX takes whole seconds. Round up so sub-second durations never
	// expire early (and always result in a positive expiry).
	seconds := int64(ttl / time.Second)
	if ttl%time.Second != 0 {
		seconds++
	}
	_, _ = c.command("SET", redisContractKeyPrefix+key, string(blob), "EX", seconds)
}

type redisReply struct {
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

func (c *redisCache) command(args ...any) (json.RawMessage, bool) {
	payload, err := json.Marshal(args)
	if err != nil || len(payload) > redisMaxRequestBytes {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheOpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	response, err := c.client.Do(req)
	if err != nil {
		return nil, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, redisMaxResponseBytes+1))
	if err != nil || len(body) > redisMaxResponseBytes {
		return nil, false
	}
	var reply redisReply
	if json.Unmarshal(body, &reply) != nil || reply.Error != "" || len(reply.Result) == 0 ||
		bytes.Equal(reply.Result, []byte("null")) {
		return nil, false
	}
	return reply.Result, true
}
