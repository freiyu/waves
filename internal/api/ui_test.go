package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// Fonts are served as they are, and only hashed asset names are cached for long.
func TestUIFontsAndLicenses(t *testing.T) {
	h := New(nil, 7317, fstest.MapFS{
		"assets/a-1.woff2": {Data: []byte("wOF2 font")},
		"licenses/OFL.txt": {Data: []byte("SIL Open Font License")},
	}, nil).Handler()
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7317"+path, nil)
		r.Header.Set("Accept-Encoding", "gzip")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	font := get("/assets/a-1.woff2")
	if font.Code != 200 || font.Header().Get("Content-Type") != "font/woff2" || font.Header().Get("Content-Encoding") != "" || font.Body.String() != "wOF2 font" {
		t.Errorf("font: %d %v %q", font.Code, font.Header(), font.Body)
	}
	lic := get("/licenses/OFL.txt")
	if lic.Code != 200 || lic.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("license: %d %v", lic.Code, lic.Header())
	}
	if missing := get("/assets/none.woff2"); missing.Code != 404 || missing.Header().Get("Cache-Control") != "" {
		t.Errorf("missing font: %d %v", missing.Code, missing.Header())
	}
}
