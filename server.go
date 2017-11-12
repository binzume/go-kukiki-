package main

import (
	"bufio"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/binzume/go-calendar"
	"github.com/binzume/go-markdown"
	"github.com/gin-gonic/gin"
)

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

func initHttpd(storage Storage) *gin.Engine {
	r := gin.Default()

	r.SetFuncMap(template.FuncMap{
		"markdown": markdownRenderer,
		"calender": printCalender,
	})
	r.LoadHTMLGlob("static/*.html")

	r.Static("/css", "./static/css")
	r.Static("/js", "./static/js")

	r.GET("/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"_status": 200, "message": "It works!"})
	})

	r.NoRoute(func(c *gin.Context) {
		page := c.Request.URL.Path
		c.Set("storage", storage)
		if storage.Exists(page + ".md") {
			// local file. TODO bytes := storage.Get(...)
			if file, ok := storage.FilePath(page + ".md"); ok {
				fmt.Print(file)
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
