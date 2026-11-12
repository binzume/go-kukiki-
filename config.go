package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config
type Config struct {
	// Title is the site-wide title.
	Title                       string
	TemplateDir                 string
	Variables                   map[string]any
	Default                     DirConfig
	Overrides                   []DirConfig
	AllowUnsafeUploadExtensions bool `toml:"allowUnsafeUploadExtensions"`
}

func (c *Config) templateDir() string {
	if c.TemplateDir == "" {
		return "static"
	}
	return c.TemplateDir
}

func (c *Config) templatePath(name string) string {
	return filepath.Join(c.templateDir(), name)
}

// DirConfig
type DirConfig struct {
	Prefix   string
	DataDir  string
	Template string
	Diary    bool
	Title    string
}

func (c *Config) siteTitle(dirConfig DirConfig) string {
	if dirConfig.Title != "" {
		return dirConfig.Title
	}
	return c.Title
}

// LoadConfig
func LoadConfig(confFile string) (*Config, error) {
	config := Config{
		Title: "Kukiki-",
		Default: DirConfig{
			Template: "template.html",
		},
	}

	_, err := toml.DecodeFile(confFile, &config)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("config file %q was not found; using default values", confFile)
		}
		return &config, nil
	}
	return &config, nil
}
