package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchFastestUseCacheOn304(t *testing.T) {
	payload := []byte(`{"nodesA":["1.1.1.1:443"]}`)
	cache := &sdkCacheFile{Entries: map[string]cacheEntry{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write(payload)
	}))
	defer ts.Close()

	b1, u, etag, lm, err := fetchFastest(context.Background(), []string{ts.URL}, cache)
	if err != nil {
		t.Fatal(err)
	}
	cache.update(u, etag, lm, b1)
	b2, _, _, _, err := fetchFastest(context.Background(), []string{ts.URL}, cache)
	if err != nil {
		t.Fatal(err)
	}
	if string(b2) != string(payload) {
		t.Fatalf("unexpected payload from cache")
	}
}

