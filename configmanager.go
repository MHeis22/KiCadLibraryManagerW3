package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Repository represents a single Git library
type Repository struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// LibraryPart is a single component entry returned by BrowseLibrary
type LibraryPart struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Repo     string `json:"repo"`
}

// DuplicateInfo describes an existing library location that matches an incoming part
type DuplicateInfo struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Repo     string `json:"repo"`
}

// HistoryItem tracks an integration event for undo purposes
type HistoryItem struct {
	ID           string   `json:"id"`
	Timestamp    int64    `json:"timestamp"`
	Filename     string   `json:"filename"`
	Category     string   `json:"category"`
	RepoName     string   `json:"repoName"`
	AddedFiles   []string `json:"addedFiles"`
	SymbolMaster string   `json:"symbolMaster"`
	SymbolBackup string   `json:"symbolBackup"`
}

// Config represents the user's saved settings
type Config struct {
	BaseLibPath     string              `json:"baseLibPath"`
	WatchDir        string              `json:"watchDir"`
	Repositories    []Repository        `json:"repositories"`
	Categories      []string            `json:"categories"`
	History         []HistoryItem       `json:"history"`
	AutoStart       bool                `json:"autoStart"`
	DefaultRepo     string              `json:"defaultRepo"`
	AutoCategoryMap map[string][]string `json:"autoCategoryMap"`
	Version         string              `json:"version,omitempty"`
}

func getConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	appDir := filepath.Join(configDir, "KiCadLibMgr")
	os.MkdirAll(appDir, os.ModePerm)
	return filepath.Join(appDir, "config.json")
}

func LoadConfig() Config {
	path := getConfigPath()
	data, err := os.ReadFile(path)

	homeDir, _ := os.UserHomeDir()
	defaultWatchDir := filepath.Join(homeDir, "Downloads")

	// Massive default dictionary for maximum accuracy out-of-the-box
	defaultCategoryMap := map[string][]string{
		"Passives":       {"resistor", "capacitor", "inductor", "ferrite", "potentiometer", "varistor", "thermistor"},
		"Connectors":     {"connector", "header", "receptacle", "plug", "jack", "terminal", "usb", "hdmi", "rj45", "socket"},
		"Semiconductors": {"diode", "transistor", "mosfet", "bjt", "igbt", "rectifier", "tvs", "zener", "triac"},
		"Power":          {"ldo", "regulator", "buck", "boost", "converter", "smps", "pmic", "dcdc"},
		"OpAmps":         {"opamp", "amplifier", "comparator", "operational"},
		"MCU":            {"mcu", "microcontroller", "microprocessor", "dsp", "fpga", "cpld"},
		"Sensors":        {"sensor", "accelerometer", "gyroscope", "magnetometer", "thermometer", "encoder"},
		"Switches":       {"switch", "relay", "button", "toggle", "dip", "tactile"},
		"Logic":          {"buffer", "transceiver", "inverter", "gate", "flip-flop", "latch", "multiplexer"},
	}

	// Default configuration
	defaultConfig := Config{
		BaseLibPath:     "",
		WatchDir:        defaultWatchDir,
		Repositories:    []Repository{{Name: "CustomLibs", URL: ""}},
		Categories:      []string{"MCU", "Power", "Connectors", "Passives", "OpAmps", "Semiconductors", "Sensors", "Switches", "Logic"},
		History:         []HistoryItem{},
		AutoStart:       false,
		AutoCategoryMap: defaultCategoryMap,
	}

	if err != nil {
		return defaultConfig
	}

	var c Config
	err = json.Unmarshal(data, &c)
	if err != nil || len(c.Categories) == 0 {
		return defaultConfig
	}

	if len(c.AutoCategoryMap) == 0 {
		c.AutoCategoryMap = defaultCategoryMap
	}

	// Ensure at least one repo exists for safety
	if len(c.Repositories) == 0 {
		c.Repositories = defaultConfig.Repositories
	}

	// Ensure watch dir defaults if somehow empty
	if c.WatchDir == "" {
		c.WatchDir = defaultWatchDir
	}

	return c
}

func SaveConfig(c Config) error {
	path := getConfigPath()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (c *Config) AddCustomCategory(category string) {
	// 1. Ensure it's in the UI Categories list
	existsInUI := false
	for _, existing := range c.Categories {
		if strings.EqualFold(existing, category) {
			existsInUI = true
			break
		}
	}
	if !existsInUI {
		c.Categories = append(c.Categories, category)
	}

	// 2. Ensure it's in the AutoCategoryMap (The Matcher)
	if c.AutoCategoryMap == nil {
		c.AutoCategoryMap = make(map[string][]string)
	}

	// If the map already has keywords for this category, we don't need to re-seed
	if _, existsInMap := c.AutoCategoryMap[category]; existsInMap {
		return
	}

	// 3. Seed the keywords since they are missing
	keyword := strings.ToLower(category)
	singular := keyword
	if strings.HasSuffix(keyword, "s") && !strings.HasSuffix(keyword, "ss") {
		singular = strings.TrimSuffix(keyword, "s")
	}

	keywords := []string{keyword}
	if singular != keyword {
		keywords = append(keywords, singular)
	}

	c.AutoCategoryMap[category] = keywords
}

func (c *Config) RenameCategory(oldName string, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("new category name cannot be empty")
	}
	for _, existing := range c.Categories {
		if strings.EqualFold(existing, newName) && !strings.EqualFold(existing, oldName) {
			return fmt.Errorf("category %q already exists", newName)
		}
	}
	found := false
	for i, cat := range c.Categories {
		if cat == oldName {
			c.Categories[i] = newName
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("category %q not found", oldName)
	}
	if c.AutoCategoryMap != nil {
		if keywords, ok := c.AutoCategoryMap[oldName]; ok {
			c.AutoCategoryMap[newName] = keywords
			delete(c.AutoCategoryMap, oldName)
		}
	}
	return nil
}

func (c *Config) DeleteCategory(name string) error {
	if len(c.Categories) <= 1 {
		return fmt.Errorf("cannot delete the last remaining category")
	}
	found := false
	filtered := c.Categories[:0]
	for _, cat := range c.Categories {
		if cat == name {
			found = true
		} else {
			filtered = append(filtered, cat)
		}
	}
	if !found {
		return fmt.Errorf("category %q not found", name)
	}
	c.Categories = filtered
	if c.AutoCategoryMap != nil {
		delete(c.AutoCategoryMap, name)
	}
	return nil
}
