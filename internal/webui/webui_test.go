package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFallbackAndHeaders(t *testing.T) {
	h := Handler()
	for _, c := range []struct {
		path, cache string
		code        int
	}{
		{"/", "no-cache", 200},
		{"/some/spa/route", "no-cache", 200},
		{"/_app/immutable/missing.js", "no-cache", 404},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))
		if rec.Code != c.code || rec.Header().Get("Cache-Control") != c.cache {
			t.Errorf("%s: code %d cache %q", c.path, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}
