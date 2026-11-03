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
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

func convertMarkdown(reader io.Reader, writer markdown.DocWriter) error {
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
		err := md.Convert(d.Content, d.Writer)
		d.Writer.End(n)
		return err
	})

	return md.Convert(reader, writer)
}

func convertMarkdownReader(reader io.Reader, writer markdown.DocWriter) error {
	return convertMarkdown(reader, writer)
}

type markdownSegment struct {
	input   *io.PipeWriter
	writer  *markdownLinkWriter
	section int
	done    chan error
}

func isCodeFenceLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

var diarySectionIDPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-`)
var diaryDateSectionPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
var calendarDaySpanPattern = regexp.MustCompile(`<span(?: class='([^']*)')?>(.*?)</span>`)

func sectionHTMLID(name string, diaryMode bool) string {
	if diaryMode && len(name) > 8 && diarySectionIDPattern.MatchString(name) {
		return name[8:]
	}
	return name
}

func startMarkdownSegment(out io.Writer, sectionName string, sectionEditURL func(string) string, diaryMode bool) *markdownSegment {
	return startMarkdownSegmentWithHeadingOffset(out, sectionName, sectionEditURL, diaryMode, 0)
}

func startMarkdownSegmentWithHeadingOffset(out io.Writer, sectionName string, sectionEditURL func(string) string, diaryMode bool, baseHeadingOffset int) *markdownSegment {
	headingOffset := 0
	if sectionName != "" {
		headingOffset = 2
	}
	headingOffset += baseHeadingOffset
	writer := &markdownLinkWriter{DocWriter: markdown.NewHTMLWriterWithHeadingOffset(out, headingOffset)}
	section := 0
	if sectionName != "" {
		sectionID := sectionHTMLID(sectionName, diaryMode)
		section = writer.Block(markdown.MarkupData{
			Attributes: markdown.Attributes(".section #" + sectionID),
		})
		// The section body writer offsets Markdown headings by two levels. Use
		// level 0 here so the section title remains the enclosing h2.
		title := writer.Heading(0, markdown.MarkupData{})
		fmt.Fprintf(out, "<a href='#%s'>", template.HTMLEscapeString(sectionID))
		writer.Write(sectionName)
		io.WriteString(out, "</a>")
		if sectionEditURL != nil {
			url := template.HTMLEscapeString(sectionEditURL(sectionName))
			fmt.Fprintf(out, " [<a class='section-edit' href='%s'>Edit</a>]", url)
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
	return renderMarkdownReaderWithHeadingOffset(reader, out, sectionEditURL, diaryMode, 0, nil)
}

func renderMarkdownReaderWithSections(reader io.Reader, out io.Writer, sectionEditURL func(string) string, diaryMode bool, sectionFound func(string)) error {
	return renderMarkdownReaderWithHeadingOffset(reader, out, sectionEditURL, diaryMode, 0, sectionFound)
}

func renderMarkdownReaderWithHeadingOffset(reader io.Reader, out io.Writer, sectionEditURL func(string) string, diaryMode bool, headingOffset int, sectionFound func(string)) error {
	scanner := bufio.NewScanner(reader)
	segment := startMarkdownSegmentWithHeadingOffset(out, "", sectionEditURL, diaryMode, headingOffset)
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
				if sectionFound != nil {
					sectionFound(section.Name)
				}
				if err := segment.close(); err != nil {
					return err
				}
				segment = startMarkdownSegmentWithHeadingOffset(out, section.Name, sectionEditURL, diaryMode, headingOffset)
				continue
			}
		}

		if _, err := io.WriteString(segment.input, line+"\n"); err != nil {
			_ = segment.close()
			return err
		}
		if isCodeFenceLine(line) {
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

func renderMarkdownFile(path string, out io.Writer, headingOffsets ...int) error {
	fp, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fp.Close()

	headingOffset := 0
	if len(headingOffsets) > 0 {
		headingOffset = headingOffsets[0]
	}
	return renderMarkdownReaderWithHeadingOffset(fp, out, nil, false, headingOffset, nil)
}

func dataFileName(storage fs.FS, requestPath string) (string, bool) {
	path := strings.TrimPrefix(requestPath, "/")
	if path == "" || strings.HasSuffix(requestPath, "/") || strings.HasPrefix(path, "/") {
		return "", false
	}
	name, err := normalizeFSPath(path)
	if err != nil {
		return "", false
	}
	info, err := fs.Stat(storage, name)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return name, true
}

func parseIntDefault(str string, defvalue int) int {
	v, err := strconv.ParseInt(str, 10, 32)
	if err != nil {
		return defvalue
	}
	return int(v)
}

type templateContext struct {
	Request     *http.Request
	FileSystem  fs.FS
	Input       io.Reader
	InputPath   string
	PagePath    string
	Diary       bool
	DiaryDates  map[string]struct{}
	DiaryMonths []DiaryMonth
	DiaryYears  []DiaryYear
}

type DiaryMonth struct {
	Label string
	Month string
	URL   string
}

type DiaryYear struct {
	Year   string
	Months []DiaryMonth
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

	var sectionFound func(string)
	if c.Diary {
		if c.DiaryDates == nil {
			c.DiaryDates = make(map[string]struct{})
		}
		sectionFound = func(name string) {
			if diaryDateSectionPattern.MatchString(name) {
				c.DiaryDates[name] = struct{}{}
			}
		}
	}

	var out bytes.Buffer
	if err := renderMarkdownReaderWithSections(reader, &out, sectionEditURL, c.Diary, sectionFound); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil
}

// markdownFile renders a fixed Markdown file for use in a template. The
// template owns the path, so it can be used for shared content such as a menu.
func markdownFile(path string, headingOffsets ...int) (template.HTML, error) {
	var out bytes.Buffer
	if err := renderMarkdownFile(path, &out, headingOffsets...); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return template.HTML(out.String()), nil
}

func markdownStorageFile(storage fs.FS, path string, headingOffsets ...int) (template.HTML, error) {
	file, err := openStorageFile(storage, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer file.Close()

	headingOffset := 0
	if len(headingOffsets) > 0 {
		headingOffset = headingOffsets[0]
	}
	var out bytes.Buffer
	if err := renderMarkdownReaderWithHeadingOffset(file, &out, nil, false, headingOffset, nil); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil
}

func printCalendar(c *templateContext) template.HTML {
	var storage fs.FS
	diaryMode := false
	pagePath := ""
	var diaryDates map[string]struct{}
	if c != nil {
		storage = c.FileSystem
		diaryMode = c.Diary
		pagePath = c.PagePath
		diaryDates = c.DiaryDates
	}
	navigation := printDiaryMonthNavigation(c)
	cal := calendar.NewCalendar()
	if month, err := time.ParseInLocation("2006-01", navigation.Label, time.Local); err == nil {
		cal.Date = month
	}
	cal.LinkFunc = func(t time.Time) string {
		path := fmt.Sprintf("%04d-%02d-%02d", t.Year(), t.Month(), t.Day())
		if diaryMode && pagePath != "" {
			if _, ok := diaryDates[path]; !ok {
				return ""
			}
			return "/" + pagePath + "#" + sectionHTMLID(path, true)
		}
		if storage == nil || existsFile(storage, path+".md") {
			return path
		}
		return ""
	}
	calendarHTML := addCalendarTodayClass(cal.Html(), cal.Date, time.Now())
	return template.HTML(calendarHTML)
}

type DiaryMonthNavigation struct {
	Label       string
	PreviousURL string
	NextURL     string
}

func printDiaryMonthNavigation(c *templateContext) DiaryMonthNavigation {
	month := time.Now()
	diaryMode := false
	pagePath := ""
	var pageMonth time.Time
	pageMonthValid := false
	if c != nil {
		diaryMode = c.Diary
		pagePath = c.PagePath
	}
	if diaryMode && pagePath != "" {
		monthName := pagePath
		if separator := strings.LastIndexByte(monthName, '/'); separator >= 0 {
			monthName = monthName[separator+1:]
		}
		if parsedMonth, err := time.ParseInLocation("2006-01", monthName, time.Local); err == nil {
			pageMonth = parsedMonth
			pageMonthValid = true
			month = pageMonth
		}
	}
	navigation := DiaryMonthNavigation{Label: month.Format("2006-01")}
	if diaryMode && pagePath != "" && pageMonthValid {
		navigation.PreviousURL = diaryMonthURL(pagePath, pageMonth.AddDate(0, -1, 0))
		navigation.NextURL = diaryMonthURL(pagePath, pageMonth.AddDate(0, 1, 0))
	}
	return navigation
}

func addCalendarTodayClass(markup string, month, today time.Time) string {
	if month.Year() != today.Year() || month.Month() != today.Month() {
		return markup
	}
	day := strconv.Itoa(today.Day())
	return calendarDaySpanPattern.ReplaceAllStringFunc(markup, func(value string) string {
		match := calendarDaySpanPattern.FindStringSubmatch(value)
		if len(match) != 3 || calendarDayText(match[2]) != day {
			return value
		}
		class := strings.TrimSpace(match[1])
		if class != "" {
			class += " "
		}
		return "<span class='" + class + "today'>" + match[2] + "</span>"
	})
}

func calendarDayText(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "<a ") {
		if start := strings.IndexByte(content, '>'); start >= 0 {
			content = content[start+1:]
		}
		if end := strings.LastIndex(content, "</a>"); end >= 0 {
			content = content[:end]
		}
	}
	return strings.TrimSpace(content)
}

func diaryMonthURL(pagePath string, month time.Time) string {
	return diaryFileURL(pageDirectory(pagePath), month.Format("2006-01"))
}

func diaryFileURL(directory, name string) string {
	directory = strings.Trim(directory, "/")
	if directory == "" {
		return "/" + name
	}
	return "/" + directory + "/" + name
}

func diaryMonths(directory string, files []string) []DiaryMonth {
	months := make([]DiaryMonth, 0, len(files))
	for _, file := range files {
		label := strings.TrimSuffix(file, ".md")
		months = append(months, DiaryMonth{
			Label: label,
			Month: label[len(label)-2:],
			URL:   diaryFileURL(directory, label),
		})
	}
	return months
}

// diaryYears groups the already-sorted diary files by year. The files are
// supplied newest-first, while the months within each year are displayed in
// calendar order to match the diary archive layout.
func buildDiaryYears(directory string, files []string) []DiaryYear {
	years := make([]DiaryYear, 0, len(files))
	indexes := make(map[string]int)
	for _, file := range files {
		label := strings.TrimSuffix(file, ".md")
		if len(label) != len("2006-01") {
			continue
		}
		year := label[:4]
		index, ok := indexes[year]
		if !ok {
			index = len(years)
			indexes[year] = index
			years = append(years, DiaryYear{Year: year})
		}
		years[index].Months = append(years[index].Months, DiaryMonth{
			Label: label,
			Month: label[len(label)-2:],
			URL:   diaryFileURL(directory, label),
		})
	}
	for i := range years {
		months := years[i].Months
		for left, right := 0, len(months)-1; left < right; left, right = left+1, right-1 {
			months[left], months[right] = months[right], months[left]
		}
	}
	return years
}

func filterDiaryYears(years []DiaryYear, startDate string) []DiaryYear {
	startDate = strings.TrimSpace(startDate)
	if startDate == "" {
		return years
	}

	startMonth := ""
	for _, layout := range []string{"2006-01-02", "2006-01"} {
		if date, err := time.ParseInLocation(layout, startDate, time.Local); err == nil {
			startMonth = date.Format("2006-01")
			break
		}
	}
	if startMonth == "" {
		return years
	}

	filtered := make([]DiaryYear, 0, len(years))
	for _, year := range years {
		months := make([]DiaryMonth, 0, len(year.Months))
		for _, month := range year.Months {
			if month.Label >= startMonth {
				months = append(months, month)
			}
		}
		if len(months) > 0 {
			filtered = append(filtered, DiaryYear{Year: year.Year, Months: months})
		}
	}
	return filtered
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
func listDiaryFiles(storage fs.FS, directory string) []string {
	directory = strings.Trim(directory, "/")
	readDirPath := directory
	if readDirPath == "" {
		readDirPath = "."
	}
	entries, err := fs.ReadDir(storage, readDirPath)
	if err != nil {
		return nil
	}

	files := make([]string, 0)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		if !diaryFilePattern.MatchString(name) {
			continue
		}
		files = append(files, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	return files
}

func latestDiaryFile(storage fs.FS, directory string) string {
	files := listDiaryFiles(storage, directory)
	if len(files) == 0 {
		return ""
	}
	return diaryFileURL(directory, files[0])
}

func pageDirectory(page string) string {
	if separator := strings.LastIndexByte(page, '/'); separator >= 0 {
		return strings.Trim(page[:separator], "/")
	}
	return ""
}

// sectionPage returns the file that contains a section. In diary mode, a
// date-named section belongs to the monthly diary file named by its year and
// month, regardless of the page file supplied by the editor.
func sectionPage(config *Config, page, section string) string {
	pageConfigPath := "/" + strings.TrimPrefix(page, "/")
	if !directoryConfig(config, pageConfigPath).Diary || !diaryDateSectionPattern.MatchString(section) {
		return page
	}

	directory := pageDirectory(page)
	monthPage := section[:7] + ".md"
	if directory == "" {
		return monthPage
	}
	return directory + "/" + monthPage
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
func resolvePageFileWithDiaryFiles(storage fs.FS, requestPath string, config *Config) (string, DirConfig, []string, error) {
	page, err := pageFile(requestPath)
	if err != nil {
		return "", DirConfig{}, nil, err
	}
	conf := directoryConfig(config, page)
	var diaryFiles []string
	if conf.Diary && strings.HasSuffix(page, "/index.md") {
		directory := pageDirectory(page)
		diaryFiles = listDiaryFiles(storage, directory)
		if len(diaryFiles) > 0 {
			page = diaryFileURL(directory, diaryFiles[0])
		} else {
			page = ""
		}
	} else if conf.Diary {
		diaryFiles = listDiaryFiles(storage, pageDirectory(page))
	}
	return page, conf, diaryFiles, nil
}

func resolvePageFile(storage fs.FS, requestPath string, config *Config) (string, DirConfig, error) {
	page, conf, _, err := resolvePageFileWithDiaryFiles(storage, requestPath, config)
	return page, conf, err
}

// Breadcrumb is one item in the page hierarchy shown above a page.
type Breadcrumb struct {
	Title string
	URL   string
}

func storagePageTitle(storage fs.FS, page string) string {
	data, err := readStorageFile(storage, page)
	if err != nil {
		return ""
	}
	return NewParser(bytes.NewReader(data)).ParseHeader()["title"]
}

func buildBreadcrumbs(storage fs.FS, pagePath, siteTitle string) []Breadcrumb {
	return buildBreadcrumbsWithConfig(storage, pagePath, siteTitle, nil)
}

func buildBreadcrumbsWithConfig(storage fs.FS, pagePath, siteTitle string, config *Config) []Breadcrumb {
	return buildBreadcrumbsWithConfigAndDiaryFiles(storage, pagePath, siteTitle, config, "", nil)
}

func buildBreadcrumbsWithConfigAndDiaryFiles(storage fs.FS, pagePath, siteTitle string, config *Config, diaryDirectory string, diaryFiles []string) []Breadcrumb {
	cleanPath := strings.Trim(pagePath, "/")
	if cleanPath == "" || cleanPath == "index.md" {
		return nil
	}

	parts := strings.Split(cleanPath, "/")
	breadcrumbs := []Breadcrumb{{Title: siteTitle, URL: "/"}}
	if breadcrumbs[0].Title == "" {
		breadcrumbs[0].Title = "Home"
	}

	directoryCount := len(parts) - 1
	if strings.HasSuffix(cleanPath, "/index.md") || cleanPath == "index.md" {
		directoryCount--
	}
	for i := 0; i < directoryCount; i++ {
		directory := strings.Join(parts[:i+1], "/")
		title := parts[i]
		indexPage := "/" + directory + "/index.md"
		if config != nil && directoryConfig(config, indexPage).Diary {
			latest := ""
			if directory == diaryDirectory && diaryFiles != nil {
				if len(diaryFiles) > 0 {
					latest = diaryFileURL(directory, diaryFiles[0])
				}
			} else {
				latest = latestDiaryFile(storage, directory)
			}
			if latest != "" {
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

func editReturnURL(r *http.Request) string {
	u := *r.URL
	query := u.Query()
	query.Del("edit")
	query.Del("section")
	u.RawQuery = query.Encode()
	return u.String()
}

func diaryTodayEditURL(r *http.Request) string {
	u := *r.URL
	directory := u.Path
	if !strings.HasSuffix(directory, "/") {
		directory = strings.TrimRight(directory, "/")
		if separator := strings.LastIndexByte(directory, '/'); separator >= 0 {
			directory = directory[:separator+1]
		} else {
			directory = "/"
		}
	}
	if directory == "" {
		directory = "/"
	}
	u.Path = directory
	u.RawPath = ""
	u.Fragment = ""
	query := u.Query()
	query.Set("edit", "")
	query.Set("section", time.Now().Format("2006-01-02"))
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
		MaxAge:   int(auth.sessionTTLSeconds()),
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

func setLastModified(w http.ResponseWriter, info fs.FileInfo) {
	if info == nil || info.ModTime().IsZero() {
		return
	}
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
}

func setLastModifiedFromStorage(w http.ResponseWriter, storage fs.FS, path string) {
	name, err := normalizeFSPath(path)
	if err != nil {
		return
	}
	info, err := fs.Stat(storage, name)
	if err != nil {
		return
	}
	setLastModified(w, info)
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

func notFoundTemplatePath(config *Config) string {
	return config.templatePath("404.md")
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

func initHttpd(storage fs.FS, config *Config) http.Handler {
	templates, err := template.New("root").Funcs(template.FuncMap{
		"markdown": markdownRenderer,
		"markdownFile": func(path string, headingOffsets ...int) (template.HTML, error) {
			return markdownStorageFile(storage, path, headingOffsets...)
		},
		"calender":             printCalendar,
		"diaryMonthNavigation": printDiaryMonthNavigation,
		"diaryYears":           filterDiaryYears,
	}).ParseGlob(filepath.Join(config.templateDir(), "*.html"))
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()

	mux.Handle("/css/", http.StripPrefix("/css/", http.FileServerFS(os.DirFS(filepath.Join(config.templateDir(), "css")))))
	mux.Handle("/js/", http.StripPrefix("/js/", http.FileServerFS(os.DirFS(filepath.Join(config.templateDir(), "js")))))

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
			sectionName := firstQueryValue(sectionName)
			section, err := readSection(storage, sectionPage(config, page, sectionName), sectionName)
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
		if data, err := readStorageFile(storage, page); err == nil {
			setLastModifiedFromStorage(w, storage, page)
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
			sectionName := firstQueryValue(sectionValues)
			targetPage := sectionPage(config, page, sectionName)
			pageConfigPath := "/" + strings.TrimPrefix(targetPage, "/")
			prepend := directoryConfig(config, pageConfigPath).Diary
			if err := updateSection(storage, targetPage, sectionName, *parsed.Text, parsed.UpdateUnixMS, prepend); err != nil {
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
			message := "stored"
			if *parsed.Text == "" {
				message = "deleted"
			}
			writeJSON(w, http.StatusOK, map[string]any{"_status": 200, "message": message})
			return
		}
		if *parsed.Text == "" {
			err := removeStoredFile(storage, page)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				if errors.Is(err, ErrWritableFS) {
					writeJSON(w, http.StatusNotImplemented, map[string]any{"_status": 501, "message": "page deletion is not supported"})
				} else {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"_status": 500, "message": "failed to delete page"})
				}
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"_status": 200, "message": "deleted"})
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
		if file, ok := dataFileName(storage, r.URL.Path); ok {
			http.ServeFileFS(w, r, storage, file)
			return
		}
		page, conf, diaryFiles, err := resolvePageFileWithDiaryFiles(storage, r.URL.Path, config)
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
			editorTemplates, err := template.ParseFiles(config.templatePath("editor.html"))
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			executeTemplate(w, editorTemplates, http.StatusOK, "editor.html", templateData(config, map[string]any{
				"title":     "It works!",
				"siteTitle": config.siteTitle(config.Default),
				"page":      pageName,
				"returnURL": editReturnURL(r),
				"section":   r.URL.Query().Get("section"),
				"loggedIn":  true,
			}))
			return
		}

		breadcrumbPage := page
		if requestedPage, err := pageFile(r.URL.Path); err == nil {
			breadcrumbPage = requestedPage
		}
		if existsFile(storage, page) {
			storageName, err := normalizeFSPath(page)
			if err == nil {
				fp, err := storage.Open(storageName)
				if err != nil {
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					return
				}
				defer fp.Close()
				if info, err := fp.Stat(); err == nil {
					setLastModified(w, info)
				}
				parser := NewParser(fp)
				headers := parser.ParseHeader()
				pagePath := strings.TrimSuffix(strings.TrimPrefix(page, "/"), ".md")
				pageContext := &templateContext{
					Request:    r,
					FileSystem: storage,
					Input:      parser,
					InputPath:  storageName,
					PagePath:   pagePath,
					Diary:      conf.Diary,
					DiaryYears: buildDiaryYears(pageDirectory(page), diaryFiles),
				}
				loggedIn := isLoggedIn(r)
				pageData := templateData(config, map[string]any{
					"title":     headers["title"],
					"siteTitle": config.siteTitle(conf),
					"loggedIn":  loggedIn,
					"breadcrumbs": buildBreadcrumbsWithConfigAndDiaryFiles(
						storage,
						breadcrumbPage,
						config.siteTitle(conf),
						config,
						pageDirectory(breadcrumbPage),
						diaryFiles,
					),
					"ctx":    pageContext,
					"config": conf,
				})
				if loggedIn {
					pageData["editURL"] = editPageURL(r)
					if conf.Diary {
						pageData["todayEditURL"] = diaryTodayEditURL(r)
					}
				}
				executeTemplate(w, templates, http.StatusOK, conf.Template, pageData)
				return
			}
		}

		notFoundContext := &templateContext{
			Request:    r,
			FileSystem: storage,
			InputPath:  notFoundTemplatePath(config),
			PagePath:   strings.TrimSuffix(strings.TrimPrefix(page, "/"), ".md"),
			Diary:      conf.Diary,
			DiaryYears: buildDiaryYears(pageDirectory(page), diaryFiles),
		}
		loggedIn := isLoggedIn(r)
		notFoundData := templateData(config, map[string]any{
			"title":     "404 Not Found",
			"siteTitle": config.siteTitle(conf),
			"loggedIn":  loggedIn,
			"breadcrumbs": buildBreadcrumbsWithConfigAndDiaryFiles(
				storage,
				breadcrumbPage,
				config.siteTitle(conf),
				config,
				pageDirectory(breadcrumbPage),
				diaryFiles,
			),
			"ctx":       notFoundContext,
			"config":    conf,
		})
		if loggedIn && conf.Diary {
			notFoundData["todayEditURL"] = diaryTodayEditURL(r)
		}
		executeTemplate(w, templates, http.StatusNotFound, conf.Template, notFoundData)
	})

	return loggingMiddleware(mux)
}

func main() {
	port := flag.Int("p", 8080, "http port")
	listenInterface := ""
	flag.StringVar(&listenInterface, "listen", "", "http listen interface/address")
	flag.StringVar(&listenInterface, "i", "", "http listen interface/address (alias for -listen)")
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

	addr := net.JoinHostPort(listenInterface, strconv.Itoa(*port))
	log.Printf("start server. address: %s", addr)
	server := &http.Server{
		Addr:    addr,
		Handler: initHttpd(NewLocalStorage(*dbpath), config),
	}
	log.Fatal(server.ListenAndServe())
}
