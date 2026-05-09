package sdk

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type remoteNodePayload struct {
	NodesA []string `json:"nodesA"`
	NodesB []string `json:"nodesB"`
	NodesC []string `json:"nodesC"`
	NodesD []string `json:"nodesD"`
	NodesE []string `json:"nodesE"`
}
type fetchResult struct {
	url     string
	payload []byte
	etag    string
	lm      string
	err     error
}

func resolveControlPlane(ctx context.Context, p *SecretPayload, cache *sdkCacheFile) (NodeGroups, *sdkCacheFile, error) {
	urls := buildNodeDataURLs(p)
	raw, used, etag, lm, err := fetchFastest(ctx, urls, cache)
	if err != nil {
		return NodeGroups{}, cache, err
	}
	plain, err := decryptNodeData(raw, p.AESKey)
	if err != nil {
		return NodeGroups{}, cache, err
	}
	var nodes remoteNodePayload
	if err := json.Unmarshal(plain, &nodes); err != nil {
		return NodeGroups{}, cache, fmt.Errorf("parse node json: %w", err)
	}
	groups := sanitizeNodeGroups(NodeGroups{A: nodes.NodesA, B: nodes.NodesB, C: nodes.NodesC, D: nodes.NodesD, E: nodes.NodesE})
	if cache == nil {
		cache = &sdkCacheFile{Entries: map[string]cacheEntry{}}
	}
	cache.update(used, etag, lm, raw)
	return groups, cache, nil
}

func buildNodeDataURLs(p *SecretPayload) []string {
	date := beijingNow().Format("20060102")
	cosMD5 := shortMD5(date + p.AppName + "cos" + p.SDKVersion)
	ossMD5 := shortMD5(date + p.AppName + "oss" + p.SDKVersion)
	zosMD5 := shortMD5(date + p.AppName + "zos" + p.SDKVersion)
	return []string{
		fmt.Sprintf("https://%s-%s.cos.accelerate.myqcloud.com/%s.dat", cosMD5[:16], p.COSAppID, cosMD5[16:]),
		fmt.Sprintf("https://%s.oss-accelerate.aliyuncs.com/%s.dat", ossMD5[:16], ossMD5[16:]),
		fmt.Sprintf("https://%s.jiangsu-10.zos.ctyun.cn/%s.dat", zosMD5[:16], zosMD5[16:]),
	}
}
func beijingNow() time.Time {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST-8", 8*3600)
	}
	return time.Now().In(loc)
}
func shortMD5(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) } //nolint:gosec

func fetchFastest(ctx context.Context, urls []string, cache *sdkCacheFile) ([]byte, string, string, string, error) {
	if len(urls) == 0 {
		return nil, "", "", "", fmt.Errorf("empty urls")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan fetchResult, len(urls))
	client := &http.Client{Timeout: 8 * time.Second}
	for _, u := range urls {
		url := u
		go func() { ch <- fetchByHeadThenGet(ctx, client, url, cache) }()
	}
	var firstErr error
	for i := 0; i < len(urls); i++ {
		r := <-ch
		if r.err == nil && len(r.payload) > 0 {
			cancel()
			return r.payload, r.url, r.etag, r.lm, nil
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", r.url, r.err)
		}
	}
	return nil, "", "", "", firstErr
}

func fetchByHeadThenGet(ctx context.Context, client *http.Client, url string, cache *sdkCacheFile) fetchResult {
	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return fetchResult{url: url, err: err}
	}
	if cache != nil && cache.Entries != nil {
		if ce, ok := cache.Entries[url]; ok {
			if ce.ETag != "" {
				headReq.Header.Set("If-None-Match", ce.ETag)
			}
			if ce.LastModified != "" {
				headReq.Header.Set("If-Modified-Since", ce.LastModified)
			}
		}
	}

	headResp, err := client.Do(headReq)
	if err != nil {
		return fetchResult{url: url, err: err}
	}
	defer headResp.Body.Close()

	switch headResp.StatusCode {
	case http.StatusNotModified:
		if payload, ok := cache.getPayload(url); ok {
			return fetchResult{url: url, payload: payload}
		}
		return fetchResult{url: url, err: fmt.Errorf("head 304 but no cache payload")}
	case http.StatusOK:
		// updated (or first-time), fetch full content by GET
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		// Some object storage endpoints may not support HEAD well.
		// Fallback to GET with conditional headers.
	default:
		return fetchResult{url: url, err: fmt.Errorf("head status=%d", headResp.StatusCode)}
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fetchResult{url: url, err: err}
	}
	if cache != nil && cache.Entries != nil {
		if ce, ok := cache.Entries[url]; ok {
			if ce.ETag != "" {
				getReq.Header.Set("If-None-Match", ce.ETag)
			}
			if ce.LastModified != "" {
				getReq.Header.Set("If-Modified-Since", ce.LastModified)
			}
		}
	}

	getResp, err := client.Do(getReq)
	if err != nil {
		return fetchResult{url: url, err: err}
	}
	defer getResp.Body.Close()

	if getResp.StatusCode == http.StatusNotModified {
		if payload, ok := cache.getPayload(url); ok {
			return fetchResult{url: url, payload: payload}
		}
		return fetchResult{url: url, err: fmt.Errorf("get 304 but no cache payload")}
	}
	if getResp.StatusCode != http.StatusOK {
		return fetchResult{url: url, err: fmt.Errorf("get status=%d", getResp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(getResp.Body, 2*1024*1024))
	if err != nil {
		return fetchResult{url: url, err: err}
	}
	return fetchResult{
		url:     url,
		payload: body,
		etag:    getResp.Header.Get("ETag"),
		lm:      getResp.Header.Get("Last-Modified"),
	}
}

func decryptNodeData(raw []byte, aesKey string) ([]byte, error) {
	if plain, ok := tryPlainJSON(raw); ok {
		return plain, nil
	}
	enc := raw
	trimmed := strings.TrimSpace(string(raw))
	if b, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		if plain, ok := tryPlainJSON(b); ok {
			return plain, nil
		}
		enc = b
	}
	key := normalizeAESKey(aesKey)
	if len(enc) > 12 {
		if plain, err := tryDecryptGCM(enc, key); err == nil {
			return plain, nil
		}
	}
	if len(enc) > aes.BlockSize {
		if plain, err := tryDecryptCBC(enc, key); err == nil {
			return plain, nil
		}
	}
	return nil, fmt.Errorf("unsupported or invalid encrypted node data")
}

func tryPlainJSON(raw []byte) ([]byte, bool) {
	s := strings.TrimSpace(string(raw))
	s = strings.TrimPrefix(s, "\uFEFF")
	if s == "" || !strings.HasPrefix(s, "{") || !json.Valid([]byte(s)) {
		return nil, false
	}
	return []byte(s), true
}
func normalizeAESKey(aesKey string) []byte {
	b := []byte(aesKey)
	switch len(b) {
	case 16, 24, 32:
		return b
	default:
		sum := sha256.Sum256(b)
		return sum[:]
	}
}
func tryDecryptGCM(enc, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(enc) <= gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short for gcm")
	}
	return gcm.Open(nil, enc[:gcm.NonceSize()], enc[gcm.NonceSize():], nil)
}
func tryDecryptCBC(enc, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(enc) < aes.BlockSize {
		return nil, fmt.Errorf("ciphertext too short for cbc")
	}
	iv, ct := enc[:aes.BlockSize], enc[aes.BlockSize:]
	if len(ct)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("cbc ciphertext not aligned")
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)
	return pkcs7Unpad(out, aes.BlockSize)
}
func pkcs7Unpad(data []byte, block int) ([]byte, error) {
	if len(data) == 0 || len(data)%block != 0 {
		return nil, fmt.Errorf("invalid pkcs7 size")
	}
	p := int(data[len(data)-1])
	if p <= 0 || p > block || p > len(data) {
		return nil, fmt.Errorf("invalid pkcs7 padding")
	}
	for i := len(data) - p; i < len(data); i++ {
		if int(data[i]) != p {
			return nil, fmt.Errorf("invalid pkcs7 padding bytes")
		}
	}
	return data[:len(data)-p], nil
}

func sanitizeNodeGroups(g NodeGroups) NodeGroups {
	return NodeGroups{
		A: sanitizeNodeList(g.A),
		B: sanitizeNodeList(g.B),
		C: sanitizeNodeList(g.C),
		D: sanitizeNodeList(g.D),
		E: sanitizeNodeList(g.E),
	}
}
func sanitizeNodeList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, s := range in {
		n := strings.TrimSpace(s)
		if n == "" {
			continue
		}
		if _, _, err := parseEndpoint(n); err != nil {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

