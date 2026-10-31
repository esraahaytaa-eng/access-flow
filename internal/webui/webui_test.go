package webui_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/esraahaytaa-eng/access-flow/internal/webui"
)

func TestHandlerServesFilesAndFallsBackToIndex(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
		// A directory with its own page (the Swagger UI) is served as that page.
		"swagger/index.html": {Data: []byte("<html>docs</html>")},
	}
	cases := []struct{ path, want string }{
		{"/", "<html>app</html>"},
		{"/any/route", "<html>app</html>"},
		{"/dashboard", "<html>app</html>"},
		{"/assets", "<html>app</html>"},
		{"/index.html", ""}, // redirected to "/" by net/http
		{"/assets/app.js", "console.log(1)"},
		{"/swagger/", "<html>docs</html>"},
		{"/swagger", ""}, // redirected to "/swagger/" by net/http
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			webui.Handler(dist).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if tc.want == "" {
				if rec.Code != http.StatusMovedPermanently {
					t.Fatalf("status = %d; want 301", rec.Code)
				}
				return
			}
			if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
				t.Fatalf("got %d %q; want 200 %q", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

// Regression: a page cached before a rebuild asks for an old hashed bundle; it must
// get a 404, not index.html served as JavaScript (blank page with a MIME error).
func TestHandlerReturns404ForMissingAssets(t *testing.T) {
	dist := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}
	for _, p := range []string{"/assets/index-old.js", "/assets/sub/missing.css"} {
		rec := httptest.NewRecorder()
		webui.Handler(dist).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d; want 404", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<html>app</html>") {
			t.Errorf("%s: served index.html", p)
		}
	}
}

// PERF-009: Vite's assets carry a content hash, so browsers may keep them; the page itself
// is never cached, so a rebuild is picked up at once.
func TestHashedAssetsAreCachedAndThePageIsNot(t *testing.T) {
	dist := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "assets/app-1a2b.js": {Data: []byte("x")}}
	for path, want := range map[string]string{"/assets/app-1a2b.js": "public, max-age=31536000, immutable", "/": "no-cache", "/route": "no-cache"} {
		rec := httptest.NewRecorder()
		webui.Handler(dist).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control = %q; want %q", path, got, want)
		}
	}
}
