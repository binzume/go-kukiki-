package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/binzume/go-calendar"
	"github.com/binzume/go-markdown"
	"github.com/gin-gonic/gin"
)

var pagePrefixBlacklist = []string{"/api/", "static", "/css/", "/js/"}

func parseIntDefault(str string, defvalue int) int {
	v, err := strconv.ParseInt(str, 10, 32)
	if err != nil {
		return defvalue
	}
	return int(v)
}

func markdownRenderer(c *gin.Context) template.HTML {
	var fp *os.File
	var err error

	path := c.GetString("inputfile")

	fp, err = os.Open(path)
	if err != nil {
		panic(err)
	}
	defer fp.Close()

	// out := os.Stdout
	// out := &bytes.Buffer{}
	out := c.Writer

	scanner := bufio.NewScanner(fp)
	writer := markdown.NewHTMLWriter(out)
	err = markdown.Convert(scanner, writer)
	if err != nil {
		panic(err)
	}
	writer.Close()

	if err := scanner.Err(); err != nil {
		panic(err)
	}
	return "" // template.HTML(out.String())
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

func pageFile(page string) string {
	return page + ".md"
}

func initHttpd(storage Storage) *gin.Engine {
	r := gin.Default()

	r.SetFuncMap(template.FuncMap{
		"markdown": markdownRenderer,
		"calender": printCalender,
	})
	r.LoadHTMLGlob("static/*.html")

	r.Static("/css", "./static/css")
	r.Static("/js", "./static/js")

	r.GET("/api/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"_status": 200, "message": "It works!"})
	})

	r.GET("/api/pages/:page", func(c *gin.Context) {
		page := pageFile(c.Param("page"))
		if data, err := storage.Get(page); err == nil {
			c.Data(http.StatusOK, "text/plain", data)
		} else {
			c.JSON(http.StatusOK, gin.H{"_status": 404, "message": "page not found"})
		}
	})

	r.POST("/api/pages/:page", func(c *gin.Context) {
		page := pageFile(c.Param("page"))
		for _, prefix := range pagePrefixBlacklist {
			if strings.HasPrefix(page, prefix) {
				c.JSON(http.StatusOK, gin.H{"_status": 400, "message": "bad page name"})
				return
			}
		}

		if data, err := ioutil.ReadAll(c.Request.Body); err == nil {
			var parsed map[string]string
			err = json.Unmarshal(data, &parsed)
			if err != nil {
				fmt.Print(err)
			}
			storage.Store(page, []byte(parsed["text"]))
		}

		c.JSON(http.StatusOK, gin.H{"_status": 200, "message": "stored"})
	})

	r.NoRoute(func(c *gin.Context) {
		page := pageFile(c.Request.URL.Path)
		if _, ok := c.GetQuery("edit"); ok {
			pageName := strings.TrimLeft(c.Request.URL.Path, "/")
			c.HTML(http.StatusOK, "editor.html", gin.H{"title": "It works!", "page": pageName, "ctx": c})
			return
		}
		c.Set("storage", storage)
		if storage.Exists(page) {
			// local file. TODO bytes := storage.Get(...)
			if file, ok := storage.FilePath(page); ok {
				c.Set("inputfile", file)
				c.HTML(http.StatusOK, "template.html", gin.H{"title": "It works!", "ctx": c})
				return
			}
		}
		c.Set("inputfile", "static/404.md")
		c.HTML(http.StatusOK, "template.html", gin.H{"title": "404 Not Found", "ctx": c})
	})

	return r
}

func main() {
	port := flag.Int("p", 8080, "http port")
	// dbtype := flag.String("t", "leveldb", "datastore type")
	dbpath := flag.String("d", "data", "datastore uri(s) or path")
	flag.Parse()

	gin.SetMode(gin.ReleaseMode)
	log.Printf("start server. port: %d", *port)
	initHttpd(NewLoacalStorage(*dbpath)).Run(":" + fmt.Sprint(*port))
}
