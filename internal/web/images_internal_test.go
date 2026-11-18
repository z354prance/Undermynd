package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestImageLinks(t *testing.T) {
	if imgURL("https://evil.example/x.jpg") != "" || imgURL("") != "" || imgURL("file:///etc/passwd") != "" {
		t.Fatal("a foreign address got a link")
	}
	l := imgURL("https://image.tmdb.org/t/p/w500/abc.jpg")
	if !strings.HasPrefix(l, "/img?") {
		t.Fatal(l)
	}
	if imgURL("https://r2.theaudiodb.com/images/a.jpg") == "" || imgURL("https://ia800.us.archive.org/x.jpg") == "" {
		t.Fatal("subdomains of image hosts")
	}
	s := &Server{}
	u, _ := url.Parse(l)
	q := u.Query()
	q.Set("u", "https://image.tmdb.org/t/p/w500/other.jpg")
	rec := httptest.NewRecorder()
	s.image(rec, httptest.NewRequest(http.MethodGet, "/img?"+q.Encode(), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a changed address with the old signature: %d", rec.Code)
	}
}
