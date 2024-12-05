// Package main provides XML TV guide data fetching from Zap2it
package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"gopkg.in/ini.v1"
)

const (
	defaultTimeout     = 30 * time.Second
	defaultHistoryDays = 14
	defaultLanguage    = "en-us"
	zap2itLoginURL     = "https://tvlistings.zap2it.com/api/user/login"
	zap2itGridURL      = "https://tvlistings.zap2it.com/api/grid"
	zap2itProvidersURL = "https://tvlistings.zap2it.com/gapzap_webapi/api/Providers/getPostalCodeProviders"

	// Time constants
	hoursInDay        = 24
	secondsInHour     = 3600
	secondsInHalfHour = 1800
	guideHours        = 336 // 14 days
)

// Config holds the application configuration
type Config struct {
	Username       string
	Password       string
	Country        string
	ZipCode        string
	Language       string
	LineupID       string
	HeadendID      string
	Device         string
	HistoricalDays int
}

// Guide represents the main application structure
type Guide struct {
	config     Config
	client     *http.Client
	token      string
	headendID  string
	outputFile string
	xmlGuide   TV
}

// TV represents the XML TV guide structure
type TV struct {
	XMLName           xml.Name    `xml:"tv"`
	SourceInfoURL     string      `xml:"source-info-url,attr"`
	SourceInfoName    string      `xml:"source-info-name,attr"`
	GeneratorInfoName string      `xml:"generator-info-name,attr"`
	GeneratorInfoURL  string      `xml:"generator-info-url,attr"`
	Channels          []Channel   `xml:"channel"`
	Programmes        []Programme `xml:"programme"`
}

// Channel represents a TV channel
type Channel struct {
	ID          string   `xml:"id,attr"`
	DisplayName []string `xml:"display-name"`
	Icon        *Icon    `xml:"icon,omitempty"`
}

// Programme represents a TV programme
type Programme struct {
	Start      string     `xml:"start,attr"`
	Stop       string     `xml:"stop,attr"`
	Channel    string     `xml:"channel,attr"`
	Title      []Title    `xml:"title"`
	SubTitle   *SubTitle  `xml:"sub-title,omitempty"`
	Desc       *Desc      `xml:"desc,omitempty"`
	Categories []Category `xml:"category,omitempty"`
}

// Title represents a programme title
type Title struct {
	Lang string `xml:"lang,attr,omitempty"`
	Text string `xml:",chardata"`
}

// SubTitle represents a programme subtitle
type SubTitle struct {
	Lang string `xml:"lang,attr,omitempty"`
	Text string `xml:",chardata"`
}

// Desc represents a programme description
type Desc struct {
	Lang string `xml:"lang,attr,omitempty"`
	Text string `xml:",chardata"`
}

// Category represents a programme category
type Category struct {
	Lang string `xml:"lang,attr,omitempty"`
	Text string `xml:",chardata"`
}

// Icon represents a channel icon
type Icon struct {
	Src string `xml:"src,attr"`
}

// NewGuide creates a new Guide instance
func NewGuide(configPath, outputFile string) (*Guide, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return &Guide{
		config: cfg,
		client: &http.Client{
			Timeout: defaultTimeout,
		},
		outputFile: outputFile,
	}, nil
}

// loadConfig loads the configuration from the INI file
func loadConfig(path string) (Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Config{}, fmt.Errorf("config file does not exist: %s", path)
	}

	cfg, err := ini.Load(path)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config: %w", err)
	}

	return Config{
		Username:       cfg.Section("creds").Key("username").String(),
		Password:       cfg.Section("creds").Key("password").String(),
		Country:        cfg.Section("prefs").Key("country").String(),
		ZipCode:        cfg.Section("prefs").Key("zipCode").String(),
		Language:       cfg.Section("prefs").Key("lang").MustString(defaultLanguage),
		LineupID:       cfg.Section("lineup").Key("lineupId").String(),
		HeadendID:      cfg.Section("lineup").Key("headendId").String(),
		Device:         cfg.Section("lineup").Key("device").MustString("-"),
		HistoricalDays: cfg.Section("prefs").Key("historicalGuideDays").MustInt(defaultHistoryDays),
	}, nil
}

// authenticate performs authentication with Zap2it
func (g *Guide) authenticate(ctx context.Context) error {
	data := url.Values{
		"emailid":        {g.config.Username},
		"password":       {g.config.Password},
		"isfacebookuser": {"false"},
		"usertype":       {"0"},
		"objectid":       {""},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", zap2itLoginURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("authentication request failed: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse authentication response: %w", err)
	}

	token, ok := result["token"].(string)
	if !ok {
		return fmt.Errorf("token not found in response")
	}
	g.token = token

	if props, ok := result["properties"].(map[string]interface{}); ok {
		if headendID, ok := props["2004"].(string); ok {
			g.headendID = headendID
		}
	}

	return nil
}

// fetchGuideData fetches guide data for a specific time
func (g *Guide) fetchGuideData(ctx context.Context, timestamp int64) (map[string]interface{}, error) {
	params := url.Values{
		"Activity_ID":  {"1"},
		"FromPage":     {"TV Guide"},
		"AffiliateId":  {"gapzap"},
		"token":        {g.token},
		"aid":          {"gapzap"},
		"lineupId":     {g.config.LineupID},
		"timespan":     {"3"},
		"headendId":    {g.config.HeadendID},
		"country":      {g.config.Country},
		"device":       {g.config.Device},
		"postalCode":   {g.config.ZipCode},
		"isOverride":   {"true"},
		"time":         {fmt.Sprintf("%d", timestamp)},
		"pref":         {"m,p"},
		"userId":       {"-"},
		"languagecode": {g.config.Language},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", zap2itGridURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch guide data: %w", err)
	}
	defer resp.Body.Close()

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse guide data: %w", err)
	}

	return data, nil
}

func (z *Guide) FindID() error {
	urlStr := fmt.Sprintf("https://tvlistings.zap2it.com/gapzap_webapi/api/Providers/getPostalCodeProviders/%s/%s/gapzap/%s",
		z.config.Country,
		z.config.ZipCode,
		z.config.Language)

	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error loading provider IDs: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %v", err)
	}

	var idVars map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &idVars); err != nil {
		return fmt.Errorf("error parsing JSON: %v", err)
	}

	fmt.Printf("%-15s|%-40s|%-15s|%-15s|%-25s|%-15s\n", "type", "name", "location", "headendID", "lineupId", "device")
	for _, p := range idVars["Providers"].([]interface{}) {
		provider := p.(map[string]interface{})
		fmt.Printf("%-15s|%-40s|%-15s|%-15s|%-25s|%-15s\n",
			provider["type"],
			provider["name"],
			provider["location"],
			provider["headendId"],
			provider["lineupId"],
			provider["device"])
	}
	return nil
}

// BuildGuide builds the complete TV guide
func (g *Guide) BuildGuide(ctx context.Context) error {
	if err := g.authenticate(ctx); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	g.initializeGuide()

	startTime := time.Now().Add(-24 * time.Hour).Unix()
	startTime -= startTime % secondsInHalfHour
	endTime := startTime + (guideHours * secondsInHour)

	for currentTime := startTime; currentTime < endTime; currentTime += 3 * secondsInHour {
		data, err := g.fetchGuideData(ctx, currentTime)
		if err != nil {
			return fmt.Errorf("failed to fetch guide data: %w", err)
		}

		if len(g.xmlGuide.Channels) == 0 {
			if err := g.processChannels(data); err != nil {
				return fmt.Errorf("failed to process channels: %w", err)
			}
		}

		if err := g.processProgrammes(data); err != nil {
			return fmt.Errorf("failed to process programmes: %w", err)
		}
	}

	if err := g.writeGuide(); err != nil {
		return fmt.Errorf("failed to write guide: %w", err)
	}

	return g.manageHistoricalFiles()
}

// initializeGuide initializes the XML TV guide structure
func (g *Guide) initializeGuide() {
	g.xmlGuide = TV{
		SourceInfoURL:     "http://tvlistings.zap2it.com/",
		SourceInfoName:    "zap2it",
		GeneratorInfoName: "zap2itXMLTV",
		GeneratorInfoURL:  "https://github.com/spf13/zap2itxmltv",
	}
}

// processChannels processes channel data from the API response
func (g *Guide) processChannels(data map[string]interface{}) error {
	channels, ok := data["channels"].([]interface{})
	if !ok {
		return fmt.Errorf("invalid channels data format")
	}

	for _, c := range channels {
		channel, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		ch := Channel{
			ID: channel["channelId"].(string),
			DisplayName: []string{
				fmt.Sprintf("%s %s", channel["channelNo"], channel["callSign"]),
				channel["channelNo"].(string),
				channel["callSign"].(string),
			},
		}

		if thumbnail, ok := channel["thumbnail"].(string); ok {
			thumbnailURL := "http://" + strings.TrimLeft(strings.Split(thumbnail, "?")[0], "/")
			ch.Icon = &Icon{Src: thumbnailURL}
		}

		g.xmlGuide.Channels = append(g.xmlGuide.Channels, ch)
	}

	return nil
}

// processProgrammes processes programme data from the API response
func (g *Guide) processProgrammes(data map[string]interface{}) error {
	channels, ok := data["channels"].([]interface{})
	if !ok {
		return fmt.Errorf("invalid channels data format")
	}

	for _, c := range channels {
		channel, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		channelID, ok := channel["channelId"].(string)
		if !ok {
			continue
		}

		events, ok := channel["events"].([]interface{})
		if !ok {
			continue
		}

		for _, e := range events {
			event, ok := e.(map[string]interface{})
			if !ok {
				continue
			}

			prog, err := g.buildProgramme(event, channelID)
			if err != nil {
				log.Printf("Warning: failed to build programme: %v", err)
				continue
			}

			g.xmlGuide.Programmes = append(g.xmlGuide.Programmes, prog)
		}
	}

	return nil
}

// buildProgramme builds a Programme struct from event data
func (g *Guide) buildProgramme(event map[string]interface{}, channelID string) (Programme, error) {
	prog := Programme{Channel: channelID}

	startTime, ok := event["startTime"].(string)
	if !ok {
		return Programme{}, fmt.Errorf("invalid start time")
	}
	prog.Start = formatDateTime(startTime)

	endTime, ok := event["endTime"].(string)
	if !ok {
		return Programme{}, fmt.Errorf("invalid end time")
	}
	prog.Stop = formatDateTime(endTime)

	program, ok := event["program"].(map[string]interface{})
	if !ok {
		return Programme{}, fmt.Errorf("invalid program data")
	}

	title, ok := program["title"].(string)
	if !ok {
		return Programme{}, fmt.Errorf("invalid title")
	}
	prog.Title = []Title{{Lang: g.config.Language, Text: title}}

	if episodeTitle, ok := program["episodeTitle"].(string); ok && episodeTitle != "" {
		prog.SubTitle = &SubTitle{Lang: g.config.Language, Text: episodeTitle}
	}

	desc := "Unavailable"
	if shortDesc, ok := program["shortDesc"].(string); ok && shortDesc != "" {
		desc = shortDesc
	}
	prog.Desc = &Desc{Lang: g.config.Language, Text: desc}

	return prog, nil
}

// formatDateTime formats a date-time string for XML output
func formatDateTime(dt string) string {
	dt = strings.ReplaceAll(dt, "-", "")
	dt = strings.ReplaceAll(dt, "T", "")
	dt = strings.ReplaceAll(dt, ":", "")
	return strings.Replace(dt, "Z", " +0000", 1)
}

// writeGuide writes the guide to the output file
func (g *Guide) writeGuide() error {
	file, err := os.Create(g.outputFile)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	encoder := xml.NewEncoder(file)
	encoder.Indent("", "  ")
	return encoder.Encode(g.xmlGuide)
}

// manageHistoricalFiles manages historical guide files
func (g *Guide) manageHistoricalFiles() error {
	// Create historical copy
	timestamp := time.Now().Format(".20060102150405")
	histFile := strings.TrimSuffix(g.outputFile, ".xmltv") + timestamp + ".xmltv"

	if err := copyFile(g.outputFile, histFile); err != nil {
		return fmt.Errorf("failed to create historical copy: %w", err)
	}

	// Clean old files
	return g.cleanHistoricalFiles()
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

// cleanHistoricalFiles removes historical files older than the configured retention period
func (g *Guide) cleanHistoricalFiles() error {
	dir := filepath.Dir(g.outputFile)
	cutoff := time.Now().AddDate(0, 0, -g.config.HistoricalDays)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".xmltv") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				log.Printf("Warning: failed to remove old file %s: %v", entry.Name(), err)
			}
		}
	}

	return nil
}

func main() {
	var (
		configFile string
		outputFile string
		findID     bool
	)

	pflag.StringVarP(&configFile, "configfile", "c", "./zap2itconfig.ini", "Path to config file")
	pflag.StringVarP(&outputFile, "outputfile", "o", "xmlguide.xmltv", "Path to output file")
	pflag.BoolVarP(&findID, "findid", "f", false, "Find Headendid / lineupid")
	pflag.Parse()

	guide, err := NewGuide(configFile, outputFile)
	if err != nil {
		log.Fatalf("Failed to initialize guide: %v", err)
	}

	if findID {
		if err := guide.FindID(); err != nil {
			log.Fatalf("Failed to find IDs: %v", err)
		}
		os.Exit(0)
	}

	ctx := context.Background()
	if err := guide.BuildGuide(ctx); err != nil {
		log.Fatalf("Failed to build guide: %v", err)
	}
}
