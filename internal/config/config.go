package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

type Feed struct {
	URL     string `yaml:"url"`
	Label   string `yaml:"label"`
	Enabled *bool  `yaml:"enabled"`

	// Include and Exclude are case-insensitive regular expressions (plain
	// keywords work too) matched against the article title.
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`

	include []*regexp.Regexp
	exclude []*regexp.Regexp
}

// Matches reports whether a title passes the feed's filters: it must match at
// least one include pattern (if any) and no exclude pattern.
func (f Feed) Matches(title string) bool {
	if len(f.include) > 0 && !anyMatch(f.include, title) {
		return false
	}
	return !anyMatch(f.exclude, title)
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	res := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, err
		}
		res = append(res, re)
	}
	return res, nil
}

func (f Feed) IsEnabled() bool {
	return f.Enabled == nil || *f.Enabled
}

type Config struct {
	MaxAgeDays           int    `yaml:"max_age_days"`
	MaxInitialItems      int    `yaml:"max_initial_items"`
	ArchiveRetentionDays int    `yaml:"archive_retention_days"`
	ClearArchiveOnSync   bool   `yaml:"clear_archive_on_sync"`
	DisableDateSort      bool   `yaml:"disable_date_sort"`
	Feeds                []Feed `yaml:"feeds"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(cfg.Feeds) == 0 {
		return nil, fmt.Errorf("config has no feeds")
	}
	for i := range cfg.Feeds {
		f := &cfg.Feeds[i]
		if f.URL == "" {
			return nil, fmt.Errorf("feed %d missing url", i)
		}
		var err error
		if f.include, err = compilePatterns(f.Include); err != nil {
			return nil, fmt.Errorf("feed %d invalid include pattern: %w", i, err)
		}
		if f.exclude, err = compilePatterns(f.Exclude); err != nil {
			return nil, fmt.Errorf("feed %d invalid exclude pattern: %w", i, err)
		}
	}
	if cfg.MaxAgeDays == 0 {
		cfg.MaxAgeDays = 1
	}
	return &cfg, nil
}
