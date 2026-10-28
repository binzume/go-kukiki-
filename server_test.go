package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/binzume/go-markdown"
)

func TestServer(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	config := &Config{Default: DirConfig{Template: "template.html"}}

	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Errorf("get failed. %v (%v)", err, ts.URL)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("invalid response. %v", res)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		t.Errorf("type: %v", res.Header.Get("Content-Type"))
	}
}

func TestDataFileIsServed(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	if err := storage.Store("asset.txt", []byte("raw asset")); err != nil {
		t.Fatalf("storage.Store returned an error: %v", err)
	}
	config := &Config{Default: DirConfig{Template: "template.html"}}
	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/asset.txt")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response failed: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if string(body) != "raw asset" {
		t.Errorf("body = %q, want %q", body, "raw asset")
	}
}

func TestMissingPageReturnsNotFound(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	config := &Config{Default: DirConfig{Template: "template.html"}}
	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/docs/missing")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response failed: %v", err)
	}
	if !strings.Contains(string(body), "404 Not Found") {
		t.Errorf("response does not contain the 404 page: %s", body)
	}
}

func enableTestAuth(t *testing.T) *http.Cookie {
	previousAuth := auth
	auth = &AuthConfig{
		SessionSecret: "test-session-secret",
		Accounts:      []AccountConfig{{Name: "admin", AuthType: "INSECURE", Secret: "password"}},
	}
	t.Cleanup(func() { auth = previousAuth })
	return &http.Cookie{
		Name:  SessionCookieName,
		Value: auth.CreateSessionString(&auth.Accounts[0]),
		Path:  "/",
	}
}

func TestPageAPI(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	config := &Config{Default: DirConfig{Template: "template.html"}}
	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()
	cookie := enableTestAuth(t)
	do := func(method, url string, body io.Reader) (*http.Response, error) {
		request, err := http.NewRequest(method, url, body)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(cookie)
		return http.DefaultClient.Do(request)
	}

	rootRes, err := do(http.MethodPost, ts.URL+"/api/pages/", strings.NewReader(`{"text":"# Root"}`))
	if err != nil {
		t.Fatalf("root post failed: %v", err)
	}
	rootRes.Body.Close()
	if rootRes.StatusCode != http.StatusOK {
		t.Fatalf("root post status = %d, want %d", rootRes.StatusCode, http.StatusOK)
	}
	if data, err := storage.Get("/index.md"); err != nil || string(data) != "# Root" {
		t.Fatalf("stored root page = %q, err = %v", data, err)
	}

	res, err := do(
		http.MethodPost,
		ts.URL+"/api/pages/docs/guide",
		strings.NewReader(`{"text":"# Guide"}`),
	)
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("post status = %d, want %d: %s", res.StatusCode, http.StatusOK, body)
	}
	res.Body.Close()

	if data, err := storage.Get("/docs/guide.md"); err != nil || string(data) != "# Guide" {
		t.Fatalf("stored page = %q, err = %v", data, err)
	}

	res, err = http.Get(ts.URL + "/api/pages/docs/guide")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "# Guide" {
		t.Fatalf("get response = (%d, %q), want (%d, %q)", res.StatusCode, body, http.StatusOK, "# Guide")
	}

	res, err = do(http.MethodPost, ts.URL+"/api/pages/docs/bad", strings.NewReader("{"))
	if err != nil {
		t.Fatalf("invalid post failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid JSON status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}

	res, err = do(http.MethodPost, ts.URL+"/api/pages/docs/bad", strings.NewReader(`{"title":"missing text"}`))
	if err != nil {
		t.Fatalf("missing text post failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("missing text status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}

	res, err = http.Get(ts.URL + "/api/pages/docs/missing")
	if err != nil {
		t.Fatalf("missing page get failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("missing page status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}

	failing := &failingStorage{Storage: storage}
	failingServer := httptest.NewServer(initHttpd(failing, config))
	defer failingServer.Close()
	res, err = do(http.MethodPost, failingServer.URL+"/api/pages/docs/fail", strings.NewReader(`{"text":"fail"}`))
	if err != nil {
		t.Fatalf("store failure post failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotImplemented {
		t.Errorf("store failure status = %d, want %d", res.StatusCode, http.StatusNotImplemented)
	}
}

func TestFileAPI(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	config := &Config{Default: DirConfig{Template: "template.html"}}
	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()

	request, err := http.NewRequest(http.MethodPut, ts.URL+"/api/files/docs/image.bin", bytes.NewReader([]byte("image data")))
	if err != nil {
		t.Fatalf("creating unauthenticated upload request failed: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("unauthenticated upload failed: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upload status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	cookie := enableTestAuth(t)
	do := func(method, path string, body io.Reader) *http.Response {
		request, err := http.NewRequest(method, ts.URL+path, body)
		if err != nil {
			t.Fatalf("creating %s request failed: %v", method, err)
		}
		request.AddCookie(cookie)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s %s failed: %v", method, path, err)
		}
		return response
	}

	response = do(http.MethodPut, "/api/files/docs/image.bin", bytes.NewReader([]byte("image data")))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("new upload status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	response.Body.Close()
	if data, err := storage.Get("docs/image.bin"); err != nil || string(data) != "image data" {
		t.Fatalf("uploaded file = %q, err = %v", data, err)
	}

	response = do(http.MethodPut, "/api/files/docs/image.bin", bytes.NewReader([]byte("updated")))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("replacement upload status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response.Body.Close()
	if data, err := storage.Get("docs/image.bin"); err != nil || string(data) != "updated" {
		t.Fatalf("replaced file = %q, err = %v", data, err)
	}

	response = do(http.MethodPut, "/api/files/docs/guide.md", bytes.NewReader([]byte("# Guide")))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("markdown upload status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	response.Body.Close()

	response = do(http.MethodDelete, "/api/files/docs/guide.md", nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("markdown deletion status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	response.Body.Close()
	if storage.Exists("docs/guide.md") {
		t.Fatal("markdown file still exists after deletion")
	}

	response = do(http.MethodDelete, "/api/files/docs/image.bin", nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("file deletion status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	response.Body.Close()

	response = do(http.MethodDelete, "/api/files/docs/missing.bin", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("missing file deletion status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

func TestSectionAPI(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	if err := storage.Store("diary/2025-10.md", []byte("preamble\n&[today#1730000000000]\nold text\n\n&[other]\nkeep text\n")); err != nil {
		t.Fatalf("storage.Store returned an error: %v", err)
	}
	config := &Config{Default: DirConfig{Template: "template.html"}}
	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/pages/diary/2025-10?section=today")
	if err != nil {
		t.Fatalf("section get failed: %v", err)
	}
	var got struct {
		Section      string `json:"section"`
		UpdateUnixMS int64  `json:"updateUnixMS"`
		Text         string `json:"text"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		res.Body.Close()
		t.Fatalf("decoding section response failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("section get status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if got.Section != "today" || got.UpdateUnixMS != 1730000000000 || got.Text != "old text\n\n" {
		t.Errorf("section response = %#v", got)
	}

	previousAuth := auth
	auth = &AuthConfig{
		SessionSecret: "section-test-secret",
		Accounts:      []AccountConfig{{Name: "admin", AuthType: "INSECURE", Secret: "password"}},
	}
	defer func() { auth = previousAuth }()
	request, err := http.NewRequest(http.MethodPost, ts.URL+"/api/pages/diary/2025-10?section=today", strings.NewReader(`{"text":"new text","updateUnixMS":0}`))
	if err != nil {
		t.Fatalf("section save request creation failed: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: auth.CreateSessionString(&auth.Accounts[0])})
	res, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("section save failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("section save status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	data, err := storage.Get("diary/2025-10.md")
	if err != nil {
		t.Fatalf("reading updated page failed: %v", err)
	}
	want := "preamble\n&[today]\nnew text\n&[other]\nkeep text\n"
	if string(data) != want {
		t.Errorf("updated page = %q, want %q", data, want)
	}

	request, err = http.NewRequest(http.MethodPost, ts.URL+"/api/pages/diary/2025-10?section=new_section", strings.NewReader(`{"text":"added text","updateUnixMS":0}`))
	if err != nil {
		t.Fatalf("new section request creation failed: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: auth.CreateSessionString(&auth.Accounts[0])})
	res, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("new section save failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("new section save status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	data, err = storage.Get("diary/2025-10.md")
	if err != nil {
		t.Fatalf("reading page after new section failed: %v", err)
	}
	want = "preamble\n&[today]\nnew text\n&[other]\nkeep text\n&[new_section]\nadded text\n"
	if string(data) != want {
		t.Errorf("page after new section = %q, want %q", data, want)
	}
}

func TestEditDirectoryRedirect(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	config := &Config{Default: DirConfig{Template: "template.html"}}
	r := initHttpd(storage, config)
	cookie := enableTestAuth(t)

	req := httptest.NewRequest(http.MethodGet, "/dir/?edit&from=test", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusPermanentRedirect)
	}
	if got := res.Header().Get("Location"); got != "/dir/index?edit&from=test" {
		t.Fatalf("Location = %q, want %q", got, "/dir/index?edit&from=test")
	}
}

func TestExistingPageHasEditLink(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	if err := storage.Store("docs/guide.md", []byte("# Guide")); err != nil {
		t.Fatalf("storage.Store returned an error: %v", err)
	}
	config := &Config{Default: DirConfig{Template: "template.html"}}
	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()
	cookie := enableTestAuth(t)

	request, err := http.NewRequest(http.MethodGet, ts.URL+"/docs/guide?from=test", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}
	request.AddCookie(cookie)
	res, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("reading response failed: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(body), `href="/docs/guide?edit=&amp;from=test"`) {
		t.Errorf("edit link is missing from response: %s", body)
	}
	if !strings.Contains(string(body), `class="side-menu"`) || !strings.Contains(string(body), "href='/'") {
		t.Errorf("fixed Markdown menu is missing from response: %s", body)
	}
}

func TestSiteTitleIsAppendedToPageTitle(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	if err := storage.Store("docs/guide.md", []byte("# Guide")); err != nil {
		t.Fatalf("storage.Store returned an error: %v", err)
	}
	config := &Config{Default: DirConfig{Template: "template.html"}, Title: "Kukiki Site"}
	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/docs/guide")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("reading response failed: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(body), "<title>Guide - Kukiki Site</title>") {
		t.Errorf("site title was not appended to page title: %s", body)
	}
}

func TestLoadConfigSiteTitle(t *testing.T) {
	configPath := t.TempDir() + "/kukiki.toml"
	if err := os.WriteFile(configPath, []byte("[kukiki]\ntitle = \"Configured title\"\n"), 0644); err != nil {
		t.Fatalf("os.WriteFile returned an error: %v", err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned an error: %v", err)
	}
	if got := config.siteTitle(config.Default); got != "Configured title" {
		t.Errorf("site title = %q, want %q", got, "Configured title")
	}
}

func TestTemplateVariables(t *testing.T) {
	configPath := t.TempDir() + "/kukiki.toml"
	if err := os.WriteFile(configPath, []byte("[variables]\nauthor = \"binzume\"\n"), 0644); err != nil {
		t.Fatalf("os.WriteFile returned an error: %v", err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned an error: %v", err)
	}
	if got := config.Variables["author"]; got != "binzume" {
		t.Errorf("variable author = %#v, want %q", got, "binzume")
	}

	data := templateData(config, map[string]any{"title": "Page"})
	if got := data["author"]; got != "binzume" {
		t.Errorf("direct template variable = %#v, want %q", got, "binzume")
	}
	variables, ok := data["variables"].(map[string]any)
	if !ok || variables["author"] != "binzume" {
		t.Errorf("namespaced template variable = %#v, want %q", data["variables"], "binzume")
	}
	if got := templateData(config, map[string]any{"author": "page author"})["author"]; got != "page author" {
		t.Errorf("explicit template data was overwritten: %#v", got)
	}
}

func TestTemplateVariablesAreRendered(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	if err := storage.Store("index.md", []byte("# Home")); err != nil {
		t.Fatalf("storage.Store returned an error: %v", err)
	}
	config := &Config{
		Variables: map[string]any{"author": "binzume"},
		Default:   DirConfig{Template: "binzume.html"},
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	initHttpd(storage, config).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "Copyright © binzume all rights reserved.") {
		t.Errorf("template variable is missing from rendered page: %s", response.Body.String())
	}
}

func TestPageBreadcrumbs(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	pages := map[string]string{
		"index.md":                "# Home page",
		"software/index.md":       "# Software",
		"software/cppfl/index.md": "# Cppfl",
		"software/cppfl/hoge.md":  "# Hoge page",
	}
	for path, content := range pages {
		if err := storage.Store(path, []byte(content)); err != nil {
			t.Fatalf("storage.Store(%q) returned an error: %v", path, err)
		}
	}

	config := &Config{
		Title:   "Kukiki Site",
		Default: DirConfig{Template: "template.html"},
	}
	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/software/cppfl/hoge")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("reading response failed: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	got := string(body)
	start := strings.Index(got, `<nav class="breadcrumbs"`)
	if start == -1 {
		t.Fatalf("breadcrumb navigation is missing from response: %s", body)
	}
	end := strings.Index(got[start:], "</nav>")
	if end == -1 {
		t.Fatalf("breadcrumb navigation is not closed: %s", body)
	}
	got = got[start : start+end]
	wantItems := []string{
		`<a href="/">Kukiki Site</a>`,
		`<a href="/software/">Software</a>`,
		`<a href="/software/cppfl/">Cppfl</a>`,
	}
	last := -1
	for _, item := range wantItems {
		index := strings.Index(got, item)
		if index == -1 {
			t.Errorf("breadcrumb item %q is missing from response: %s", item, body)
			continue
		}
		if index <= last {
			t.Errorf("breadcrumb item %q is out of order", item)
		}
		last = index
	}
	if !strings.Contains(got, `<a href="/software/cppfl/">Cppfl</a> /`) {
		t.Errorf("breadcrumb does not keep the trailing slash: %s", got)
	}
	if strings.Contains(got, "Hoge page") {
		t.Errorf("current page title was included in breadcrumbs: %s", got)
	}
}

func TestPageBreadcrumbsFallBackToDirectoryNames(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	if err := storage.Store("software/index.md", []byte("content without a title")); err != nil {
		t.Fatalf("storage.Store returned an error: %v", err)
	}

	breadcrumbs := buildBreadcrumbs(storage, "/software/cppfl/hoge.md", "Kukiki Site")
	want := []Breadcrumb{
		{Title: "Kukiki Site", URL: "/"},
		{Title: "software", URL: "/software/"},
		{Title: "cppfl", URL: "/software/cppfl/"},
	}
	if !reflect.DeepEqual(breadcrumbs, want) {
		t.Errorf("breadcrumbs = %#v, want %#v", breadcrumbs, want)
	}
}

func TestEditingRequiresLogin(t *testing.T) {
	previousAuth := auth
	auth = &AuthConfig{
		SessionSecret: "test-session-secret",
		Accounts:      []AccountConfig{{Name: "admin", AuthType: "INSECURE", Secret: "password"}},
	}
	defer func() { auth = previousAuth }()

	storage := NewLoacalStorage(t.TempDir())
	if err := storage.Store("docs/guide.md", []byte("# Guide")); err != nil {
		t.Fatalf("storage.Store returned an error: %v", err)
	}
	config := &Config{Default: DirConfig{Template: "template.html"}}
	r := initHttpd(storage, config)
	auth = nil

	request := httptest.NewRequest(http.MethodGet, "/docs/guide", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unauthenticated page status = %d, want %d", response.Code, http.StatusOK)
	}
	if strings.Contains(response.Body.String(), "Edit") {
		t.Errorf("unauthenticated page contains an Edit link: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/docs/guide?edit", nil)
	response = httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/login.html" {
		t.Errorf("unauthenticated edit response = (%d, %q), want (302, /login.html)", response.Code, response.Header().Get("Location"))
	}

	request = httptest.NewRequest(http.MethodPost, "/api/pages/docs/guide", strings.NewReader(`{"text":"changed"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated save status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	auth = &AuthConfig{}
	request = httptest.NewRequest(http.MethodPost, "/api/pages/docs/guide", strings.NewReader(`{"text":"changed"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("empty-secret save status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	auth = &AuthConfig{
		SessionSecret: "test-session-secret",
		Accounts:      []AccountConfig{{Name: "admin", AuthType: "INSECURE", Secret: "password"}},
	}
	request = httptest.NewRequest(http.MethodGet, "/docs/guide", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: auth.CreateSessionString(&auth.Accounts[0])})
	response = httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "Edit") {
		t.Errorf("authenticated page is missing the Edit link: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/pages/docs/guide", strings.NewReader(`{"text":"changed"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: auth.CreateSessionString(&auth.Accounts[0])})
	response = httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated save status = %d, want %d", response.Code, http.StatusOK)
	}
	if data, err := storage.Get("docs/guide.md"); err != nil || string(data) != "changed" {
		t.Fatalf("saved page = %q, err = %v", data, err)
	}
}

func TestLogoutClearsSessionAndRedirects(t *testing.T) {
	previousAuth := auth
	auth = &AuthConfig{SessionSecret: "test-session-secret"}
	defer func() { auth = previousAuth }()

	request := httptest.NewRequest(http.MethodGet, "/logout", nil)
	response := httptest.NewRecorder()
	initHttpd(NewLoacalStorage(t.TempDir()), &Config{Default: DirConfig{Template: "template.html"}}).ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("logout status = %d, want %d", response.Code, http.StatusFound)
	}
	if location := response.Header().Get("Location"); location != "/" {
		t.Errorf("logout location = %q, want %q", location, "/")
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("logout set %d cookies, want 1", len(cookies))
	}
	if cookie := cookies[0]; cookie.Name != SessionCookieName || cookie.MaxAge >= 0 || cookie.Path != "/" {
		t.Errorf("logout cookie = %#v, want expired %s cookie at /", cookie, SessionCookieName)
	}
}

func TestLogoutLinkOnlyAppearsForAuthenticatedPages(t *testing.T) {
	previousAuth := auth
	auth = &AuthConfig{
		SessionSecret: "test-session-secret",
		Accounts:      []AccountConfig{{Name: "admin", AuthType: "INSECURE", Secret: "password"}},
	}
	defer func() { auth = previousAuth }()

	storage := NewLoacalStorage(t.TempDir())
	if err := storage.Store("index.md", []byte("# Home")); err != nil {
		t.Fatalf("storage.Store returned an error: %v", err)
	}
	r := initHttpd(storage, &Config{Default: DirConfig{Template: "template.html"}})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if strings.Contains(response.Body.String(), `href="/logout"`) {
		t.Errorf("unauthenticated page contains a logout link: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: auth.CreateSessionString(&auth.Accounts[0])})
	response = httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), `<nav class="page-links"`) || !strings.Contains(response.Body.String(), `<a href="/logout">ログアウト</a>`) || !strings.Contains(response.Body.String(), `>Edit</a>`) {
		t.Errorf("authenticated page is missing the logout link: %s", response.Body.String())
	}
}

type failingStorage struct {
	Storage
}

func (s *failingStorage) Store(path string, data []byte) error {
	return errors.New("store failed")
}

func TestPageFile(t *testing.T) {
	tests := map[string]string{
		"/":                "/index.md",
		"/docs/":           "/docs/index.md",
		"/about":           "/about.md",
		"/about/":          "/about/index.md",
		"/about.html":      "/about.md",
		"/docs/index.html": "/docs/index.md",
	}

	for page, want := range tests {
		got, err := pageFile(page)
		if err != nil {
			t.Errorf("pageFile(%q) returned an unexpected error: %v", page, err)
		} else if got != want {
			t.Errorf("pageFile(%q) = %q, want %q", page, got, want)
		}
	}

	for _, page := range []string{"..", "/docs/../secret", "/docs/./index", `\secret`, "foo..bar", "bad\x00page"} {
		if got, err := pageFile(page); err == nil {
			t.Errorf("pageFile(%q) = %q, want an error", page, got)
		}
	}
}

func TestLatestDiaryFile(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	for path, content := range map[string]string{
		"diary/2024-12.md":        "# December",
		"diary/2025-02.md":        "# February",
		"diary/2025-13.md":        "# Invalid month",
		"diary/2025-02.txt":       "not a diary",
		"diary/nested/2026-01.md": "not directly below diary",
	} {
		if err := storage.Store(path, []byte(content)); err != nil {
			t.Fatalf("storage.Store(%q) returned an error: %v", path, err)
		}
	}

	if got := latestDiaryFile(storage, "diary"); got != "/diary/2025-02.md" {
		t.Errorf("latestDiaryFile = %q, want %q", got, "/diary/2025-02.md")
	}
	if got := latestDiaryFile(storage, "missing"); got != "" {
		t.Errorf("latestDiaryFile for missing directory = %q, want empty", got)
	}
}

func TestDiaryDirectoryDisplaysLatestDiary(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	for path, content := range map[string]string{
		"diary/index.md":   "# Index must not be displayed",
		"diary/2024-12.md": "# Older diary",
		"diary/2025-02.md": "# Latest diary",
		"notes/index.md":   "# Notes",
	} {
		if err := storage.Store(path, []byte(content)); err != nil {
			t.Fatalf("storage.Store(%q) returned an error: %v", path, err)
		}
	}
	config := &Config{
		Default:   DirConfig{Template: "template.html"},
		Overrides: []DirConfig{{Prefix: "/diary/", Template: "template.html", Diary: true}},
	}
	ts := httptest.NewServer(initHttpd(storage, config))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/diary/")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatalf("reading response failed: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(body), "Latest diary") {
		t.Errorf("latest diary is missing from response: %s", body)
	}
	if strings.Contains(string(body), "Index must not be displayed") || strings.Contains(string(body), "Older diary") {
		t.Errorf("diary directory rendered the wrong page: %s", body)
	}
}

func TestLocalStorageRejectsInvalidPaths(t *testing.T) {
	storage := NewLoacalStorage(t.TempDir())
	for _, path := range []string{"../secret", "/docs/../secret", `..\secret`, "/", "docs//page"} {
		if err := storage.Store(path, []byte("should not be written")); err == nil {
			t.Errorf("Store(%q) unexpectedly succeeded", path)
		}
		if _, err := storage.Get(path); err == nil {
			t.Errorf("Get(%q) unexpectedly succeeded", path)
		}
		if storage.Exists(path) {
			t.Errorf("Exists(%q) unexpectedly returned true", path)
		}
		if _, ok := storage.FilePath(path); ok {
			t.Errorf("FilePath(%q) unexpectedly succeeded", path)
		}
	}
}

func TestMarkdownLinkWriter(t *testing.T) {
	var output bytes.Buffer
	writer := &markdownLinkWriter{DocWriter: markdown.NewHTMLWriter(&output)}
	err := markdown.Convert(bufio.NewScanner(strings.NewReader(
		"[local](page.md) [root](/index.md) [nested](/docs/index.md) [external](https://example.com/page.md) [query](page.md?x=1)",
	)), writer)
	writer.Close()
	if err != nil {
		t.Fatalf("markdown.Convert returned an error: %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "href='page.html'") {
		t.Errorf("local markdown link was not rewritten: %s", got)
	}
	if !strings.Contains(got, "href='/'") {
		t.Errorf("root index link was not rewritten to a directory link: %s", got)
	}
	if !strings.Contains(got, "href='/docs/'") {
		t.Errorf("nested index link was not rewritten to a directory link: %s", got)
	}
	if !strings.Contains(got, "href='https://example.com/page.md'") {
		t.Errorf("external markdown link was rewritten: %s", got)
	}
	if !strings.Contains(got, "href='page.md?x=1'") {
		t.Errorf("non-suffix markdown link was rewritten: %s", got)
	}
}

func TestBlockDirective(t *testing.T) {
	var output bytes.Buffer
	err := convertMarkdown(
		bufio.NewScanner(strings.NewReader("::: block className\n**hello**\n:::")),
		markdown.NewHTMLWriter(&output),
	)
	if err != nil {
		t.Fatalf("convertMarkdown returned an error: %v", err)
	}

	want := "<div class='className'>\n<p><strong>hello</strong></p>\n</div>"
	if got := strings.TrimSpace(output.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderMarkdownSectionsFromParserReader(t *testing.T) {
	parser := NewParser(strings.NewReader("&title:Diary\n\n&[today]\n\n# Subheading\n\nToday\n\n&[tomorrow#1730000000000]\nTomorrow"))
	headers := parser.ParseHeader()
	if headers["title"] != "Diary" {
		t.Fatalf("title = %q, want %q", headers["title"], "Diary")
	}

	var output bytes.Buffer
	if err := renderMarkdownReader(parser, &output, nil, false); err != nil {
		t.Fatalf("renderMarkdownReader returned an error: %v", err)
	}

	got := output.String()
	for _, want := range []string{
		"<div class='section' id='today'>",
		"<h1><a href='#today'>today</a></h1>",
		"<h2>Subheading</h2>",
		"<p>Today</p>",
		"<div class='section' id='tomorrow'>",
		"<h1><a href='#tomorrow'>tomorrow</a></h1>",
		"<p>Tomorrow</p>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered sections are missing %q: %s", want, got)
		}
	}
}

func TestRenderMarkdownDiarySectionIDs(t *testing.T) {
	var output bytes.Buffer
	if err := renderMarkdownReader(
		strings.NewReader("&[2026-10-27]\n\nEntry"),
		&output,
		nil,
		true,
	); err != nil {
		t.Fatalf("renderMarkdownReader returned an error: %v", err)
	}

	got := output.String()
	for _, want := range []string{
		"<div class='section' id='27'>",
		"<h1><a href='#27'>2026-10-27</a></h1>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diary section rendering is missing %q: %s", want, got)
		}
	}
}

func TestPrintCalenderIncludesMonthLabel(t *testing.T) {
	got := string(printCalender(&templateContext{
		Diary:    true,
		PagePath: "diary/2026-10",
	}))

	if !strings.Contains(got, "<h2 class='diary-calendar-month'>2026-10</h2>") {
		t.Errorf("calendar month label is missing: %s", got)
	}
	if strings.Index(got, "<h2 class='diary-calendar-month'>") > strings.Index(got, "<table class='calendar'>") {
		t.Errorf("calendar month label is not above the calendar: %s", got)
	}
}

func TestIDDirective(t *testing.T) {
	var output bytes.Buffer
	writer := markdown.NewHTMLWriter(&output)
	err := convertMarkdown(
		bufio.NewScanner(strings.NewReader(":id[hoge]{#foo}")),
		writer,
	)
	writer.Close()
	if err != nil {
		t.Fatalf("convertMarkdown returned an error: %v", err)
	}

	want := `<p><span id='foo'></span></p>`
	if got := strings.TrimSpace(output.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownFile(t *testing.T) {
	path := t.TempDir() + "/menu.md"
	if err := os.WriteFile(path, []byte("# Menu\n\n[Home](/index.md)"), 0644); err != nil {
		t.Fatalf("os.WriteFile returned an error: %v", err)
	}

	got, err := markdownFile(path)
	if err != nil {
		t.Fatalf("markdownFile returned an error: %v", err)
	}
	if !strings.Contains(string(got), "<h1>Menu</h1>") {
		t.Errorf("rendered heading is missing: %s", got)
	}
	if !strings.Contains(string(got), "href='/'") {
		t.Errorf("rendered link was not rewritten: %s", got)
	}
}
