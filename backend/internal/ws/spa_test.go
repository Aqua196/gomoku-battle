package ws

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSPAHandler(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa</html>"), 0o600))
	must(os.Mkdir(filepath.Join(dir, "assets"), 0o750))
	must(os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o600))
	// A secret next to (outside) the static root must stay unreachable.
	must(os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("nope"), 0o600))

	h := spaHandler(dir)
	cases := []struct {
		path, accept string
		status       int
		body         string
	}{
		{"/", "text/html", 200, "spa"},
		{"/game", "", 200, "spa"},                  // client route
		{"/replay/g_abc", "text/html", 200, "spa"}, // nested client route
		{"/assets/app.js", "", 200, "console.log"},
		{"/assets/missing.js", "", 404, ""}, // honest 404, not HTML
		{"/favicon.ico", "", 404, ""},       // missing file with extension
		{"/../secret.txt", "", 404, ""},
		{"/assets/../../secret.txt", "", 404, ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://x"+c.path, nil)
		req.URL.Path = c.path // keep the raw ".." segments
		if c.accept != "" {
			req.Header.Set("Accept", c.accept)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != c.status {
			t.Errorf("%s: status %d, want %d", c.path, rec.Code, c.status)
		}
		if strings.Contains(string(body), "nope") {
			t.Errorf("%s: escaped the static root", c.path)
		}
		if c.body != "" && !strings.Contains(string(body), c.body) {
			t.Errorf("%s: body %q, want it to contain %q", c.path, body, c.body)
		}
	}
}
