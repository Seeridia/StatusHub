package controlplane

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestReactConsoleAssetsAndPolicy(t *testing.T) {
	server := newTestServer(t, &repositoryStub{}, verifierStub{tenantID: testTenantID}, nil)
	get := func(path, encoding string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Accept-Encoding", encoding)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		return recorder
	}
	page := get("/ui/", "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), `id="root"`) {
		t.Fatalf("React shell missing: %d", page.Code)
	}
	csp := page.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self';") || !strings.Contains(csp, "style-src 'self';") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("CSP changed unexpectedly: %s", csp)
	}
	scripts := regexp.MustCompile(`src="(/ui/assets/[^" ]+\.js)"`).FindStringSubmatch(page.Body.String())
	if len(scripts) != 2 {
		t.Fatal("built entry script missing")
	}
	script := get(scripts[1], "gzip")
	if script.Code != 200 || script.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(script.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("hashed asset headers: %v", script.Header())
	}
	reader, err := gzip.NewReader(script.Body)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	plain := get(scripts[1], "gzip;q=0")
	if plain.Header().Get("Content-Encoding") != "" || string(compressed) != plain.Body.String() {
		t.Fatal("compression negotiation changed asset contents")
	}
	for _, path := range []string{"/ui/not-found.js", "/ui/assets/missing.css", "/ui/package.json"} {
		if response := get(path, ""); response.Code != 404 {
			t.Errorf("%s returned %d", path, response.Code)
		}
	}
	if get("/ui/icons.css", "").Code != 200 || get("/ui/legacy.html", "").Code != 200 {
		t.Fatal("icon CSS or legacy UI unavailable")
	}
}
func TestTenantSessionAndLogout(t *testing.T) {
	server := newTestServer(t, &repositoryStub{}, verifierStub{tenantID: testTenantID}, nil)
	for _, item := range []struct{ method, path string }{{"GET", "/v1/tenants/acme/session"}, {"POST", "/v1/tenants/acme/logout"}} {
		request := httptest.NewRequest(item.method, item.path, nil)
		request.Header.Set("Authorization", "Bearer valid")
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != 200 {
			t.Fatalf("%s: %d %s", item.path, recorder.Code, recorder.Body.String())
		}
		if item.method == "GET" && ((!strings.Contains(recorder.Body.String(), `"role":"owner"`) || !strings.Contains(recorder.Body.String(), `"slug":"acme"`)) || recorder.Header().Get("Cache-Control") != "no-store") {
			t.Fatal("identity or no-store missing")
		}
		cross := newTestServer(t, &repositoryStub{}, verifierStub{tenantID: "other"}, nil)
		recorder = httptest.NewRecorder()
		cross.Handler().ServeHTTP(recorder, request)
		if recorder.Code != 401 {
			t.Fatalf("cross-tenant identity status %d", recorder.Code)
		}
	}
}

func TestConsoleRootRedirect(t *testing.T) {
	server := newTestServer(t, &repositoryStub{}, verifierStub{tenantID: testTenantID}, nil)
	for _, tc := range []struct {
		method, path, location string
		status                 int
	}{
		{"GET", "/", "/ui/", http.StatusFound},
		{"HEAD", "/", "/ui/", http.StatusFound},
		{"GET", "/?tenant=statushub", "/ui/?tenant=statushub", http.StatusFound},
		{"GET", "/not-a-route", "", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status || response.Header().Get("Location") != tc.location {
			t.Errorf("%s %s: status %d, location %q", tc.method, tc.path, response.Code, response.Header().Get("Location"))
		}
	}
}
