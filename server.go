package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/binzume/go-calendar"
	"github.com/binzume/go-markdown"
)

var pagePrefixBlacklist = []string{"/api/", "static", "/css/", "/js/"}

const SessionCookieName = "KUKIKI_SESSION"

func isBlockedPage(page string) bool {
	page = strings.TrimPrefix(page, "/")
	page = strings.TrimSuffix(page, ".md")
	for _, prefix := range pagePrefixBlacklist {
		prefix = strings.Trim(prefix, "/")
		if page == prefix || strings.HasPrefix(page, prefix+"/") {
			return true
		}
	}
	return false
}

type markdownLinkWriter struct {
	markdown.DocWriter
}

func (w *markdownLinkWriter) Link(url string, title string, data markdown.MarkupData) int {
	if !strings.HasPrefix(url, "http:") && !strings.HasPrefix(url, "https:") {
		if strings.HasSuffix(url, "/index.md") {
			url = strings.TrimSuffix(url, "index.md")
		} else if strings.HasSuffix(url, ".md") {
			url = strings.TrimSuffix(url, ".md") + ".html"
		}
	}
	return w.DocWriter.Link(url, title, data)
}

func convertMarkdown(scanner *bufio.Scanner, writer markdown.DocWriter) error {
	md := markdown.NewMarkdown()
	md.RegisterDirective("id", func(d *markdown.DirectiveContext) error {
		if !strings.HasPrefix(d.Argument, "#") || len(d.Argument) == 1 {
			return fmt.Errorf("id directive argument must be a non-empty #id")
		}

		n := d.Writer.Span(markdown.MarkupData{
			Attributes: markdown.Attributes(d.Argument),
		})
		d.Writer.End(n)
		return nil
	})
	md.RegisterDirective("block", func(d *markdown.DirectiveContext) error {
		n := d.Writer.Block(markdown.MarkupData{
			Attributes: markdown.Attributes("." + d.Argument),
		})
		err := md.Convert(bufio.NewScanner(d.Content), d.Writer)
		d.Writer.End(n)
		return err
	})
	return md.Convert(scanner, writer)
}

func convertMarkdownReader(reader io.Reader, writer markdown.DocWriter) error {
	return convertMarkdown(bufio.NewScanner(reader), writer)
}

type markdownSegment struct {
	input   *io.PipeWriter
	writer  *markdownLinkWriter
	section int
	done    chan error
}

var diarySectionIDPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-`)

func sectionHTMLID(name string, diaryMode bool) string {
	if diaryMode && len(name) > 8 && diarySectionIDPattern.MatchString(name) {
		return name[8:]
	}
	return name
}

func startMarkdownSegment(out io.Writer, sectionName string, sectionEditURL func(string) string, diaryMode bool) *markdownSegment {
	headingOffset := 0
	if sectionName != "" {
		headingOffset = 1
	}
	writer := &markdownLinkWriter{DocWriter: markdown.NewHTMLWriterWithHeadingOffset(out, headingOffset)}
	section := 0
	if sectionName != "" {
		sectionID := sectionHTMLID(sectionName, diaryMode)
		section = writer.Block(markdown.MarkupData{
			Attributes: markdown.Attributes(".section #" + sectionID),
		})
		// The section body writer offsets Markdown headings by one level. Use
		// level 0 here so the section title remains the enclosing h1.
		title := writer.Heading(0, markdown.MarkupData{})
		fmt.Fprintf(out, "<a href='#%s'>", template.HTMLEscapeString(sectionID))
		writer.Write(sectionName)
		io.WriteString(out, "</a>")
		if sectionEditURL != nil {
			url := template.HTMLEscapeString(sectionEditURL(sectionName))
			fmt.Fprintf(out, " <a class='section-edit' href='%s'>Edit</a>", url)
		}
		writer.End(title)
		writer.Write("\n")
	}

	reader, input := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- convertMarkdownReader(reader, writer)
		_ = reader.Close()
	}()
	return &markdownSegment{input: input, writer: writer, section: section, done: done}
}

func (s *markdownSegment) close() error {
	closeErr := s.input.Close()
	convertErr := <-s.done
	if s.section != 0 {
		s.writer.End(s.section)
	} else {
		s.writer.Close()
	}
	if closeErr != nil {
		return closeErr
	}
	return convertErr
}

func renderMarkdownReader(reader io.Reader, out io.Writer, sectionEditURL func(string) string, diaryMode bool) error {
	scanner := bufio.NewScanner(reader)
	segment := startMarkdownSegment(out, "", sectionEditURL, diaryMode)
	inCodeFence := false

	for scanner.Scan() {
		line := scanner.Text()
		if !inCodeFence {
			section, ok, err := parseSectionHeader(line + "\n")
			if err != nil {
				_ = segment.close()
				return err
			}
			if ok {
				if err := segment.close(); err != nil {
					return err
				}
				segment = startMarkdownSegment(out, section.Name, sectionEditURL, diaryMode)
				continue
			}
		}

		if _, err := io.WriteString(segment.input, line+"\n"); err != nil {
			_ = segment.close()
			return err
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCodeFence = !inCodeFence
		}
	}

	scanErr := scanner.Err()
	segmentErr := segment.close()
	if scanErr != nil {
		return scanErr
	}
	return segmentErr
}

func renderMarkdownFile(path string, out io.Writer) error {
	fp, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fp.Close()

	return renderMarkdownReader(fp, out, nil, false)
}

func dataFilePath(storage Storage, requestPath string) (string, bool) {
	path := strings.TrimPrefix(requestPath, "/")
	if path == "" || strings.HasSuffix(requestPath, "/") || strings.HasPrefix(path, "/") {
		return "", false
	}
	if validatePath(path) != nil || !storage.Exists(path) {
		return "", false
	}
	file, ok := storage.FilePath(path)
	if !ok {
		return "", false
	}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return file, true
}

func parseIntDefault(str string, defvalue int) int {
	v, err := strconv.ParseInt(str, 10, 32)
	if err != nil {
		return defvalue
	}
	return int(v)
}

type templateContext struct {
	Request   *http.Request
	Storage   Storage
	Input     io.Reader
	InputPath string
	PagePath  string
	Diary     bool
}

func markdownRenderer(c *templateContext) (template.HTML, error) {
	if c == nil {
		return "", errors.New("missing template context")
	}

	reader := c.Input
	var file *os.File
	if reader == nil {
		var err error
		file, err = os.Open(c.InputPath)
		if err != nil {
			return "", err
		}
		defer file.Close()
		reader = file
	}

	var sectionEditURL func(string) string
	if c.Request != nil && isLoggedIn(c.Request) {
		sectionEditURL = func(name string) string {
			u := *c.Request.URL
			if c.PagePath != "" {
				u.Path = "/" + c.PagePath
			}
			query := u.Query()
			query.Set("edit", "")
			query.Set("section", name)
			u.RawQuery = query.Encode()
			return u.String()
		}
	}

	var out bytes.Buffer
	if err := renderMarkdownReader(reader, &out, sectionEditURL, c.Diary); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil
}

// markdownFile renders a fixed Markdown file for use in a template. The
// template owns the path, so it can be used for shared content such as a menu.
func markdownFile(path string) (template.HTML, error) {
	var out bytes.Buffer
	if err := renderMarkdownFile(path, &out); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil
}

func printCalender(c *templateContext) template.HTML {
	var storage Storage
	diaryMode := false
	pagePath := ""
	if c != nil {
		storage = c.Storage
		diaryMode = c.Diary
		pagePath = c.PagePath
	}
	cal := calendar.NewCalendar()
	if diaryMode && pagePath != "" {
		monthName := pagePath
		if separator := strings.LastIndexByte(monthName, '/'); separator >= 0 {
			monthName = monthName[separator+1:]
		}
		if month, err := time.ParseInLocation("2006-01", monthName, time.Local); err == nil {
			cal.Date = month
		}
	}
	cal.LinkFunc = func(t time.Time) string {
		path := fmt.Sprintf("%04d-%02d-%02d", t.Year(), t.Month(), t.Day())
		if diaryMode && pagePath != "" {
			return "/" + pagePath + "#" + sectionHTMLID(path, true)
		}
		if storage == nil || storage.Exists(path+".md") {
			return path
		}
		return ""
	}
	monthLabel := cal.Date.Format("2006-01")
	return template.HTML(fmt.Sprintf("<h2 class='diary-calendar-month'>%s</h2>\n%s", monthLabel, cal.Html()))
}

func pageFile(page string) (string, error) {
	page = strings.TrimSuffix(page, ".html")
	if err := validatePath(page); err != nil {
		return "", err
	}
	if strings.HasSuffix(page, "/") {
		return page + "index.md", nil
	}
	return page + ".md", nil
}

var diaryFilePattern = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])\.md$`)

// latestDiaryFile returns the newest monthly diary directly under directory.
// The yyyy-mm filename format makes a lexical comparison equivalent to a
// chronological comparison.
func latestDiaryFile(storage Storage, directory string) string {
	directory = strings.Trim(directory, "/")
	latest := ""
	for _, entry := range storage.List(directory) {
		name := strings.TrimLeft(entry, "/")
		name = strings.TrimPrefix(name, directory+"/")
		if !diaryFilePattern.MatchString(name) || name <= latest {
			continue
		}
		latest = name
	}
	if latest == "" {
		return ""
	}
	if directory == "" {
		return "/" + latest
	}
	return "/" + directory + "/" + latest
}

func directoryConfig(config *Config, page string) DirConfig {
	conf := config.Default
	for _, override := range config.Overrides {
		if !strings.HasPrefix(page, override.Prefix) {
			continue
		}
		if override.Template != "" {
			conf.Template = override.Template
		}
		if override.Title != "" {
			conf.Title = override.Title
		}
		conf.Diary = override.Diary
	}
	return conf
}

// resolvePageFile maps a request to the file that should be displayed. A
// diary directory uses its newest monthly diary instead of index.md.
func resolvePageFile(storage Storage, requestPath string, config *Config) (string, DirConfig, error) {
	page, err := pageFile(requestPath)
	if err != nil {
		return "", DirConfig{}, err
	}
	conf := directoryConfig(config, page)
	if conf.Diary && strings.HasSuffix(page, "/index.md") {
		page = latestDiaryFile(storage, strings.TrimSuffix(page, "index.md"))
	}
	return page, conf, nil
}

// Breadcrumb is one item in the page hierarchy shown above a page.
type Breadcrumb struct {
	Title string
	URL   string
}

func storagePageTitle(storage Storage, page string) string {
	data, err := storage.Get(page)
	if err != nil {
		return ""
	}
	return NewParser(bytes.NewReader(data)).ParseHeader()["title"]
}

func buildBreadcrumbs(storage Storage, pagePath, siteTitle string) []Breadcrumb {
	return buildBreadcrumbsWithConfig(storage, pagePath, siteTitle, nil)
}

func buildBreadcrumbsWithConfig(storage Storage, pagePath, siteTitle string, config *Config) []Breadcrumb {
	cleanPath := strings.Trim(pagePath, "/")
	if cleanPath == "" {
		return nil
	}

	parts := strings.Split(cleanPath, "/")
	breadcrumbs := []Breadcrumb{{Title: siteTitle, URL: "/"}}
	if breadcrumbs[0].Title == "" {
		breadcrumbs[0].Title = "Home"
	}

	for i := 0; i < len(parts)-1; i++ {
		directory := strings.Join(parts[:i+1], "/")
		title := parts[i]
		indexPage := "/" + directory + "/index.md"
		if config != nil && directoryConfig(config, indexPage).Diary {
			if latest := latestDiaryFile(storage, directory); latest != "" {
				title = storagePageTitle(storage, latest)
			}
		} else if indexTitle := storagePageTitle(storage, indexPage); indexTitle != "" {
			title = indexTitle
		}
		breadcrumbs = append(breadcrumbs, Breadcrumb{
			Title: title,
			URL:   "/" + directory + "/",
		})
	}

	return breadcrumbs
}

func editPageURL(r *http.Request) string {
	u := *r.URL
	query := u.Query()
	query.Set("edit", "")
	u.RawQuery = query.Encode()
	return u.String()
}

var auth *AuthConfig

func getAccount(r *http.Request) (*AccountConfig, error) {
	if auth == nil {
		return nil, errors.New("authentication is not configured")
	}
	token := r.Header.Get("Authorization")
	if strings.HasPrefix(token, "Bearer ") {
		return auth.VerifyToken(token[7:])
	}
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, err
	}
	return auth.VerifySession(c.Value)
}

func isLoggedIn(r *http.Request) bool {
	// Without an auth object or session secret, nobody is authenticated.
	if auth == nil || auth.SessionSecret == "" {
		return false
	}
	_, err := getAccount(r)
	return err == nil
}

func authHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth == nil || auth.SessionSecret == "" {
			http.Error(w, `{"error":"auth"}`, http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/tasks/") && r.Method == "POST" {
			next.ServeHTTP(w, r)
			return
		}
		a, err := getAccount(r)
		if err != nil {
			http.Error(w, `{"error":"auth"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), "ACCOUNT", a)))
	})
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.NotFound(w, r)
		return
	}

	r.ParseMultipartForm(4096)
	password := r.PostForm.Get("password")
	user := r.PostForm.Get("user")
	a, err := auth.VerifyPassword(user, password)
	if err != nil {
		http.Redirect(w, r, "login.html", 302)
		return
	}

	c := &http.Cookie{
		Name:     SessionCookieName,
		Value:    auth.CreateSessionString(a),
		MaxAge:   int(time.Hour.Seconds()) * 24 * 365,
		Secure:   r.TLS != nil,
		HttpOnly: true,
		Path:     "/",
	}
	http.SetCookie(w, c)
	http.Redirect(w, r, ".", 302)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		MaxAge:   -1,
		Secure:   r.TLS != nil,
		HttpOnly: true,
		Path:     "/",
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		response := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)

		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, status, time.Since(start))
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func firstQueryValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func writeText(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func apiPagePath(r *http.Request) string {
	page := r.PathValue("page")
	if page == "" {
		return "/"
	}
	return page
}

func executeTemplate(w http.ResponseWriter, templates *template.Template, status int, name string, data any) {
	var output bytes.Buffer
	if err := templates.ExecuteTemplate(&output, name, data); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(output.Bytes())
}

// templateData adds site-wide variables to the data passed to a template.
// Variables are available both under .variables and by their name (for
// example, .variables.author and .author). Explicit template data wins over
// a variable with the same name, so variables cannot replace built-in values
// such as .title or .ctx.
func templateData(config *Config, data map[string]any) map[string]any {
	result := make(map[string]any, len(data)+len(config.Variables)+1)
	for key, value := range data {
		result[key] = value
	}
	result["variables"] = config.Variables
	for key, value := range config.Variables {
		if _, exists := result[key]; !exists {
			result[key] = value
		}
	}
	return result
}

func initHttpd(storage Storage, config *Config) http.Handler {
	templates, err := template.New("root").Funcs(template.FuncMap{
		"markdown":     markdownRenderer,
		"markdownFile": markdownFile,
		"calender":     printCalender,
	}).ParseGlob("static/*.html")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()

	mux.Handle("/css/", http.StripPrefix("/css/", http.FileServer(http.Dir("./static/css"))))
	mux.Handle("/js/", http.StripPrefix("/js/", http.FileServer(http.Dir("./static/js"))))

	mux.HandleFunc("GET /login.html", func(w http.ResponseWriter, r *http.Request) {
		executeTemplate(w, templates, http.StatusOK, "login.html", templateData(config, map[string]any{
			"title":     "Login",
			"siteTitle": config.siteTitle(config.Default),
			"loggedIn":  isLoggedIn(r),
		}))
	})
	mux.HandleFunc("POST /login.html", loginHandler)
	mux.HandleFunc("GET /logout", logoutHandler)

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"_status": 200, "message": "It works!"})
	})

	mux.HandleFunc("PUT /api/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		if !isLoggedIn(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"_status": 401, "message": "authentication required"})
			return
		}

		name, err := normalizeFSPath(r.PathValue("path"))
		if err != nil || isBlockedPage(name) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": "bad file path"})
			return
		}
		if r.ContentLength > maxUploadSize {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"_status": 413, "message": "uploaded file is too large"})
			return
		}

		size, created, err := writeUploadedFile(storage, name, r.Body)
		if err != nil {
			switch {
			case errors.Is(err, ErrWritableFS):
				writeJSON(w, http.StatusNotImplemented, map[string]any{"_status": 501, "message": "file uploads are not supported"})
			case errors.Is(err, ErrUploadTooLarge):
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"_status": 413, "message": "uploaded file is too large"})
			case errors.Is(err, ErrNotRegularFile):
				writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": "target is not a regular file"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]any{"_status": 500, "message": "failed to store file"})
			}
			return
		}

		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{
			"_status": status,
			"path":    name,
			"size":    size,
			"url":     "/" + name,
		})
	})

	mux.HandleFunc("DELETE /api/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		if !isLoggedIn(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"_status": 401, "message": "authentication required"})
			return
		}

		name, err := normalizeFSPath(r.PathValue("path"))
		if err != nil || isBlockedPage(name) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": "bad file path"})
			return
		}
		if err := removeStoredFile(storage, name); err != nil {
			switch {
			case errors.Is(err, ErrWritableFS):
				writeJSON(w, http.StatusNotImplemented, map[string]any{"_status": 501, "message": "file deletion is not supported"})
			case os.IsNotExist(err):
				writeJSON(w, http.StatusNotFound, map[string]any{"_status": 404, "message": "file not found"})
			case errors.Is(err, ErrNotRegularFile):
				writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": "target is not a regular file"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]any{"_status": 500, "message": "failed to delete file"})
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/pages/{page...}", func(w http.ResponseWriter, r *http.Request) {
		page, err := pageFile(apiPagePath(r))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": err.Error()})
			return
		}
		if sectionName, ok := r.URL.Query()["section"]; ok {
			section, err := readSection(storage, page, firstQueryValue(sectionName))
			if err != nil {
				if errors.Is(err, ErrSectionNotFound) || os.IsNotExist(err) {
					writeJSON(w, http.StatusNotFound, map[string]any{"_status": 404, "message": "section not found"})
					return
				}
				if strings.HasPrefix(err.Error(), "invalid section name:") {
					writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": err.Error()})
					return
				}
				writeJSON(w, http.StatusInternalServerError, map[string]any{"_status": 500, "message": "failed to read section"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"section":      section.Name,
				"updateUnixMS": section.UpdateUnixMS,
				"text":         section.Body,
			})
			return
		}
		if data, err := storage.Get(page); err == nil {
			writeText(w, http.StatusOK, data)
		} else if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]any{"_status": 404, "message": "page not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"_status": 500, "message": "failed to read page"})
		}
	})

	mux.HandleFunc("POST /api/pages/{page...}", func(w http.ResponseWriter, r *http.Request) {
		if !isLoggedIn(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"_status": 401, "message": "authentication required"})
			return
		}
		page, err := pageFile(apiPagePath(r))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": err.Error()})
			return
		}
		if isBlockedPage(page) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": "bad page name"})
			return
		}

		var parsed struct {
			Text         *string `json:"text"`
			UpdateUnixMS int64   `json:"updateUnixMS"`
		}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&parsed); err != nil || parsed.Text == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": "request body must contain a text field"})
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": "request body must contain a single JSON object"})
			return
		}
		if sectionValues, ok := r.URL.Query()["section"]; ok {
			if err := updateSection(storage, page, firstQueryValue(sectionValues), *parsed.Text, parsed.UpdateUnixMS); err != nil {
				switch {
				case errors.Is(err, ErrSectionNotFound), os.IsNotExist(err):
					writeJSON(w, http.StatusNotFound, map[string]any{"_status": 404, "message": "section not found"})
				case errors.Is(err, ErrWritableFS):
					writeJSON(w, http.StatusNotImplemented, map[string]any{"_status": 501, "message": "section updates are not supported"})
				default:
					if strings.HasPrefix(err.Error(), "invalid section name:") || strings.HasPrefix(err.Error(), "invalid section update timestamp:") {
						writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": err.Error()})
					} else {
						writeJSON(w, http.StatusInternalServerError, map[string]any{"_status": 500, "message": "failed to update section"})
					}
				}
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"_status": 200, "message": "stored"})
			return
		}
		if err := writePage(storage, page, []byte(*parsed.Text)); err != nil {
			if errors.Is(err, ErrWritableFS) {
				writeJSON(w, http.StatusNotImplemented, map[string]any{"_status": 501, "message": "page updates are not supported"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"_status": 500, "message": "failed to store page"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"_status": 200, "message": "stored"})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if file, ok := dataFilePath(storage, r.URL.Path); ok {
			http.ServeFile(w, r, file)
			return
		}
		page, conf, err := resolvePageFile(storage, r.URL.Path, config)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"_status": 400, "message": err.Error()})
			return
		}
		if _, ok := r.URL.Query()["edit"]; ok {
			if !isLoggedIn(r) {
				http.Redirect(w, r, "/login.html", http.StatusFound)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/") {
				target := strings.TrimRight(r.URL.Path, "/") + "/index"
				if conf.Diary {
					target = strings.TrimRight(r.URL.Path, "/") + "/" + time.Now().Format("2006-01")
				}
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusPermanentRedirect)
				return
			}
			pageName := strings.TrimLeft(r.URL.Path, "/")
			if pageName == "" {
				pageName = "/"
			}
			editorTemplates, err := template.ParseFiles("static/editor.html")
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			executeTemplate(w, editorTemplates, http.StatusOK, "editor.html", templateData(config, map[string]any{
				"title":     "It works!",
				"siteTitle": config.siteTitle(config.Default),
				"page":      pageName,
				"section":   r.URL.Query().Get("section"),
				"loggedIn":  true,
			}))
			return
		}

		if storage.Exists(page) {
			storageName, err := normalizeFSPath(page)
			if err == nil {
				fp, err := storage.Open(storageName)
				if err != nil {
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					return
				}
				defer fp.Close()
				parser := NewParser(fp)
				headers := parser.ParseHeader()
				pagePath := strings.TrimSuffix(strings.TrimPrefix(page, "/"), ".md")
				pageContext := &templateContext{
					Request:   r,
					Storage:   storage,
					Input:     parser,
					InputPath: storageName,
					PagePath:  pagePath,
					Diary:     conf.Diary,
				}
				loggedIn := isLoggedIn(r)
				pageData := templateData(config, map[string]any{
					"title":     headers["title"],
					"siteTitle": config.siteTitle(conf),
					"loggedIn":  loggedIn,
					"breadcrumbs": buildBreadcrumbsWithConfig(
						storage,
						page,
						config.siteTitle(conf),
						config,
					),
					"ctx":    pageContext,
					"config": conf,
				})
				if loggedIn {
					pageData["editURL"] = editPageURL(r)
				}
				executeTemplate(w, templates, http.StatusOK, conf.Template, pageData)
				return
			}
		}

		executeTemplate(w, templates, http.StatusNotFound, config.Default.Template, templateData(config, map[string]any{
			"title":     "404 Not Found",
			"siteTitle": config.siteTitle(config.Default),
			"loggedIn":  isLoggedIn(r),
			"ctx": &templateContext{
				Request:   r,
				Storage:   storage,
				InputPath: "static/404.md",
			},
			"config": config.Default,
		}))
	})

	return loggingMiddleware(mux)
}

func main() {
	port := flag.Int("p", 8080, "http port")
	// dbtype := flag.String("t", "leveldb", "datastore type")
	dbpath := flag.String("d", "data", "datastore uri(s) or path")
	confFile := flag.String("c", "conf/kukiki.toml", "config file")
	flag.Parse()

	config, err := LoadConfig(*confFile)
	if err != nil {
		log.Fatal(err)
	}

	auth, err = LoadAuthConfig("conf/auth.toml")
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("start server. port: %d", *port)
	server := &http.Server{
		Addr:    ":" + fmt.Sprint(*port),
		Handler: initHttpd(NewLoacalStorage(*dbpath), config),
	}
	log.Fatal(server.ListenAndServe())
}
