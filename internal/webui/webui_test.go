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
	}
	cases := []struct{ path, want string }{
		{"/", "<html>app</html>"},
		{"/any/route", "<html>app</html>"},
		{"/dashboard", "<html>app</html>"},
		{"/assets", "<html>app</html>"},
		{"/index.html", ""}, // redirected to "/" by net/http
		{"/assets/app.js", "console.log(1)"},
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
