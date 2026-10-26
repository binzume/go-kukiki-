package main

import "github.com/BurntSushi/toml"

// Config
type Config struct {
	// Title is the site-wide title. It is also accepted in the [kukiki]
	// section below for compatibility with the existing config file layout.
	Title     string
	Kukiki    SiteConfig
	Default   DirConfig
	Overrides []DirConfig
}

// SiteConfig contains site-wide settings from the [kukiki] section.
type SiteConfig struct {
	Title string
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
	if c.Title != "" {
		return c.Title
	}
	return c.Kukiki.Title
}

// LoadConfig
func LoadConfig(confFile string) (*Config, error) {
	var config Config
	_, err := toml.DecodeFile(confFile, &config)
	if err != nil {
		return nil, err
	}
	return &config, nil

}
