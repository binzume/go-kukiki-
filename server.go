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
	"github.com/gin-gonic/gin"
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

func renderMarkdownFile(path string, out io.Writer) error {
	fp, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fp.Close()

	scanner := bufio.NewScanner(fp)
	writer := &markdownLinkWriter{DocWriter: markdown.NewHTMLWriter(out)}
	err = convertMarkdown(scanner, writer)
	writer.Close()
	if err != nil {
		return err
	}
	return scanner.Err()
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

func markdownRenderer(c *gin.Context) template.HTML {
	path := c.GetString("inputfile")
	err := renderMarkdownFile(path, c.Writer)
	if err != nil {
		panic(err)
	}
	return ""
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

func printCalender(c ...*gin.Context) template.HTML {
	var storage Storage
	if len(c) > 0 {
		if istorage, exists := c[0].Get("storage"); exists {
			storage = istorage.(Storage)
		}
	}
	cal := calendar.NewCalendar()
	cal.LinkFunc = func(t time.Time) string {
		path := fmt.Sprintf("%04d-%02d-%02d", t.Year(), t.Month(), t.Day())
		if storage == nil || storage.Exists(path+".md") {
			return path
		}
		return ""
	}
	return template.HTML(cal.Html())
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

func editPageURL(c *gin.Context) string {
	u := *c.Request.URL
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

func initHttpd(storage Storage, config *Config) *gin.Engine {
	r := gin.Default()

	r.SetFuncMap(template.FuncMap{
		"markdown":     markdownRenderer,
		"markdownFile": markdownFile,
		"calender":     printCalender,
	})
	r.LoadHTMLGlob("static/*.html")

	r.Static("/css", "./static/css")
	r.Static("/js", "./static/js")

	r.GET("/login.html", func(c *gin.Context) {
		c.HTML(http.StatusOK, "login.html", gin.H{
			"title":     "Login",
			"siteTitle": config.siteTitle(config.Default),
		})
	})
	r.POST("/login.html", func(c *gin.Context) {
		loginHandler(c.Writer, c.Request)
	})

	r.GET("/api/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"_status": 200, "message": "It works!"})
	})

	r.GET("/api/pages/*page", func(c *gin.Context) {
		page, err := pageFile(c.Param("page"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"_status": 400, "message": err.Error()})
			return
		}
		if data, err := storage.Get(page); err == nil {
			c.Data(http.StatusOK, "text/plain", data)
		} else if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"_status": 404, "message": "page not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"_status": 500, "message": "failed to read page"})
		}
	})

	r.POST("/api/pages/*page", func(c *gin.Context) {
		if !isLoggedIn(c.Request) {
			c.JSON(http.StatusUnauthorized, gin.H{"_status": 401, "message": "authentication required"})
			return
		}
		page, err := pageFile(c.Param("page"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"_status": 400, "message": err.Error()})
			return
		}
		if isBlockedPage(page) {
			c.JSON(http.StatusBadRequest, gin.H{"_status": 400, "message": "bad page name"})
			return
		}

		var parsed struct {
			Text *string `json:"text"`
		}
		decoder := json.NewDecoder(c.Request.Body)
		if err := decoder.Decode(&parsed); err != nil || parsed.Text == nil {
			c.JSON(http.StatusBadRequest, gin.H{"_status": 400, "message": "request body must contain a text field"})
			return
		}
		var extra interface{}
		if err := decoder.Decode(&extra); err != io.EOF {
			c.JSON(http.StatusBadRequest, gin.H{"_status": 400, "message": "request body must contain a single JSON object"})
			return
		}
		if err := storage.Store(page, []byte(*parsed.Text)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"_status": 500, "message": "failed to store page"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"_status": 200, "message": "stored"})
	})

	r.NoRoute(func(c *gin.Context) {
		if file, ok := dataFilePath(storage, c.Request.URL.Path); ok {
			c.File(file)
			return
		}
		page, conf, err := resolvePageFile(storage, c.Request.URL.Path, config)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"_status": 400, "message": err.Error()})
			return
		}
		if _, ok := c.GetQuery("edit"); ok {
			if !isLoggedIn(c.Request) {
				c.Redirect(http.StatusFound, "/login.html")
				return
			}
			if strings.HasSuffix(c.Request.URL.Path, "/") {
				target := strings.TrimRight(c.Request.URL.Path, "/") + "/index"
				if c.Request.URL.RawQuery != "" {
					target += "?" + c.Request.URL.RawQuery
				}
				c.Redirect(http.StatusPermanentRedirect, target)
				return
			}
			pageName := strings.TrimLeft(c.Request.URL.Path, "/")
			if pageName == "" {
				pageName = "/"
			}
			c.HTML(http.StatusOK, "editor.html", gin.H{
				"title":     "It works!",
				"siteTitle": config.siteTitle(config.Default),
				"page":      pageName,
				"ctx":       c,
			})
			return
		}
		c.Set("storage", storage)
		if storage.Exists(page) {
			// local file. TODO bytes := storage.Get(...)
			if file, ok := storage.FilePath(page); ok {
				fp, err := os.Open(file)
				if err != nil {
					panic(err)
				}
				defer fp.Close()
				parser := NewParser(fp)
				headers := parser.ParseHeader()
				c.Set("inputreader", parser)
				c.Set("inputfile", file)
				pageData := gin.H{
					"title":     headers["title"],
					"siteTitle": config.siteTitle(conf),
					"breadcrumbs": buildBreadcrumbsWithConfig(
						storage,
						page,
						config.siteTitle(conf),
						config,
					),
					"ctx":    c,
					"config": conf,
				}
				if isLoggedIn(c.Request) {
					pageData["editURL"] = editPageURL(c)
				}
				c.HTML(http.StatusOK, conf.Template, pageData)
				return
			}
		}
		c.Set("inputfile", "static/404.md")
		c.HTML(http.StatusOK, config.Default.Template, gin.H{
			"title":     "404 Not Found",
			"siteTitle": config.siteTitle(config.Default),
			"ctx":       c,
		})
	})

	return r
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

	gin.SetMode(gin.ReleaseMode)
	log.Printf("start server. port: %d", *port)
	initHttpd(NewLoacalStorage(*dbpath), config).Run(":" + fmt.Sprint(*port))
}
