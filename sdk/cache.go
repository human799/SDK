package sdk

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type cacheEntry struct {
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	PayloadB64   string `json:"payload_b64"`
	UpdatedAt    int64  `json:"updated_at_unix"`
}
type sdkCacheFile struct {
	Entries map[string]cacheEntry `json:"entries"`
}

func loadSDKCache(path string) (*sdkCacheFile, error) {
	if path == "" {
		return &sdkCacheFile{Entries: map[string]cacheEntry{}}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &sdkCacheFile{Entries: map[string]cacheEntry{}}, nil
		}
		return nil, err
	}
	var c sdkCacheFile
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse sdk cache json: %w", err)
	}
	if c.Entries == nil {
		c.Entries = map[string]cacheEntry{}
	}
	return &c, nil
}
func saveSDKCache(path string, c *sdkCacheFile) error {
	if path == "" || c == nil {
		return nil
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func (c *sdkCacheFile) getPayload(url string) ([]byte, bool) {
	if c == nil || c.Entries == nil {
		return nil, false
	}
	e, ok := c.Entries[url]
	if !ok || e.PayloadB64 == "" {
		return nil, false
	}
	b, err := base64.StdEncoding.DecodeString(e.PayloadB64)
	if err != nil {
		return nil, false
	}
	return b, true
}
func (c *sdkCacheFile) update(url, etag, lastModified string, payload []byte) {
	if c.Entries == nil {
		c.Entries = map[string]cacheEntry{}
	}
	c.Entries[url] = cacheEntry{
		URL: url, ETag: etag, LastModified: lastModified,
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
		UpdatedAt:  time.Now().Unix(),
	}
}

