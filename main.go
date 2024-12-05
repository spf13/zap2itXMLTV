package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
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

type Zap struct {
	confLocation string
	outputFile   string
	config       *ini.File
	lang         string
	zapToken     string
	headendID    string
	guideXML     Tv
}

func GuideScrape(configLocation, outputFile string) (*Zap, error) {
	if _, err := os.Stat(configLocation); os.IsNotExist(err) {
		return nil, fmt.Errorf("error: %s does not exist", configLocation)
	}
	fmt.Printf("Loading config: %s and outputting: %s\n", configLocation, outputFile)

	cfg, err := ini.Load(configLocation)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %s\nCheck file permissions", configLocation)
	}

	lang := cfg.Section("prefs").Key("lang").MustString("en")

	return &Zap{
		confLocation: configLocation,
		outputFile:   outputFile,
		config:       cfg,
		lang:         lang,
	}, nil
}

func (z *Zap) BuildAuthRequest() (*http.Request, error) {
	urlStr := "https://tvlistings.zap2it.com/api/user/login"
	data := url.Values{
		"emailid":        {z.config.Section("creds").Key("username").String()},
		"password":       {z.config.Section("creds").Key("password").String()},
		"isfacebookuser": {"false"},
		"usertype":       {"0"},
		"objectid":       {""},
	}

	req, err := http.NewRequest("POST", urlStr, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}

func (z *Zap) Authenticate() error {
	req, err := z.BuildAuthRequest()
	if err != nil {
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error connecting to tvlistings.zap2it.com: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %v", err)
	}

	var authFormVars map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &authFormVars); err != nil {
		return fmt.Errorf("error parsing JSON: %v", err)
	}

	z.zapToken = authFormVars["token"].(string)
	properties := authFormVars["properties"].(map[string]interface{})
	z.headendID = properties["2004"].(string)
	return nil
}

func (z *Zap) BuildIDRequest() (*http.Request, error) {
	urlStr := fmt.Sprintf("https://tvlistings.zap2it.com/gapzap_webapi/api/Providers/getPostalCodeProviders/%s/%s/gapzap/%s",
		z.config.Section("prefs").Key("country").String(),
		z.config.Section("prefs").Key("zipCode").String(),
		z.config.Section("prefs").Key("lang").MustString("en-us"))

	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func (z *Zap) FindID() error {
	req, err := z.BuildIDRequest()
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

func (z *Zap) BuildDataRequest(currentTime int64) (*http.Request, error) {
	lineupId := z.config.Section("lineup").Key("lineupId").MustString(z.headendID)
	headendId := z.config.Section("lineup").Key("headendId").MustString("lineupId")
	device := z.config.Section("lineup").Key("device").MustString("-")

	params := url.Values{
		"Activity_ID":  {"1"},
		"FromPage":     {"TV Guide"},
		"AffiliateId":  {"gapzap"},
		"token":        {z.zapToken},
		"aid":          {"gapzap"},
		"lineupId":     {lineupId},
		"timespan":     {"3"}, // was 3
		"headendId":    {headendId},
		"country":      {z.config.Section("prefs").Key("country").String()},
		"device":       {device},
		"postalCode":   {z.config.Section("prefs").Key("zipCode").String()},
		"isOverride":   {"true"},
		"time":         {fmt.Sprintf("%d", currentTime*1)}, // was * 1000
		"pref":         {"m,p"},
		"userId":       {"-"},
		"languagecode": {"en-us"},
		"TMSID":        {""},
		"OVDID":        {""},
	}

	urlStr := "https://tvlistings.zap2it.com/api/grid?" + params.Encode()

	// fmt.Println(urlStr)
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func (z *Zap) GetData(currentTime int64) (map[string]interface{}, error) {
	req, err := z.BuildDataRequest(currentTime)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Load Guide for time: %d\n", currentTime)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching data: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	// fmt.Println(string(bodyBytes))

	var data map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		return nil, fmt.Errorf("error parsing JSON: %v", err)
	}

	return data, nil
}

type Tv struct {
	XMLName           xml.Name    `xml:"tv"`
	SourceInfoURL     string      `xml:"source-info-url,attr"`
	SourceInfoName    string      `xml:"source-info-name,attr"`
	GeneratorInfoName string      `xml:"generator-info-name,attr"`
	GeneratorInfoURL  string      `xml:"generator-info-url,attr"`
	Channels          []Channel   `xml:"channel"`
	Programmes        []Programme `xml:"programme"`
}

type Channel struct {
	ID          string   `xml:"id,attr"`
	DisplayName []string `xml:"display-name"`
	Icon        *Icon    `xml:"icon,omitempty"`
}

type Programme struct {
	Start           string       `xml:"start,attr"`
	Stop            string       `xml:"stop,attr"`
	Channel         string       `xml:"channel,attr"`
	Title           []Title      `xml:"title"`
	SubTitle        *SubTitle    `xml:"sub-title,omitempty"`
	Desc            *Desc        `xml:"desc,omitempty"`
	Length          *Length      `xml:"length,omitempty"`
	Icon            *Icon        `xml:"icon,omitempty"`
	URL             *URL         `xml:"url,omitempty"`
	Categories      []Category   `xml:"category,omitempty"`
	EpisodeNums     []EpisodeNum `xml:"episode-num,omitempty"`
	New             *struct{}    `xml:"new,omitempty"`
	PreviouslyShown *struct{}    `xml:"previously-shown,omitempty"`
	Subtitles       *Subtitles   `xml:"subtitles,omitempty"`
	Rating          *Rating      `xml:"rating,omitempty"`
}

type Title struct {
	Lang string `xml:"lang,attr,omitempty"`
	Text string `xml:",chardata"`
}

type SubTitle struct {
	Lang string `xml:"lang,attr,omitempty"`
	Text string `xml:",chardata"`
}

type Desc struct {
	Lang string `xml:"lang,attr,omitempty"`
	Text string `xml:",chardata"`
}

type Length struct {
	Units string `xml:"units,attr"`
	Text  string `xml:",chardata"`
}

type Icon struct {
	Src string `xml:"src,attr"`
}

type URL struct {
	Text string `xml:",chardata"`
}

type Category struct {
	Lang string `xml:"lang,attr,omitempty"`
	Text string `xml:",chardata"`
}

type EpisodeNum struct {
	System string `xml:"system,attr,omitempty"`
	Text   string `xml:",chardata"`
}

type Subtitles struct {
	Type string `xml:"type,attr,omitempty"`
}

type Rating struct {
	Value *Value `xml:"value,omitempty"`
}

type Value struct {
	Text string `xml:",chardata"`
}

func (z *Zap) BuildRootEl() {
	z.guideXML = Tv{
		SourceInfoURL:     "http://tvlistings.zap2it.com/",
		SourceInfoName:    "zap2it",
		GeneratorInfoName: "zap2itXMLTV",
		GeneratorInfoURL:  "https://github.com/spf13/zap2itxmltv",
	}
}

func (z *Zap) BuildGuide() error {
	if err := z.Authenticate(); err != nil {
		return err
	}

	z.BuildRootEl()

	addChannels := true
	startTime, endTime := z.GetGuideTimes()
	for currentTime := startTime; currentTime < endTime; currentTime += 60 * 60 * 3 {
		data, err := z.GetData(currentTime)
		if err != nil {
			return err
		}
		// fmt.Println(data)
		if addChannels {
			if err := z.AddChannelsToGuide(data); err != nil {
				return err
			}
			addChannels = false
		}
		if err := z.AddEventsToGuide(data); err != nil {
			return err
		}
	}

	if err := z.WriteGuide(); err != nil {
		return err
	}
	if err := z.CopyHistorical(); err != nil {
		return err
	}
	if err := z.CleanHistorical(); err != nil {
		return err
	}

	return nil
}

func (z *Zap) GetGuideTimes() (int64, int64) {
	currentTimestamp := time.Now().Unix()
	currentTimestamp -= 60 * 60 * 24
	halfHourOffset := currentTimestamp % (60 * 30)
	currentTimestamp -= halfHourOffset
	endTimestamp := currentTimestamp + (60 * 60 * 336)
	return currentTimestamp, endTimestamp
}

func (z *Zap) AddChannelsToGuide(data map[string]interface{}) error {

	channels, ok := data["channels"].([]interface{})
	if !ok {
		return fmt.Errorf("channels not found in data")
	}
	for _, c := range channels {
		channelData := c.(map[string]interface{})
		channel, err := z.BuildChannelXML(channelData)
		if err != nil {
			return err
		}
		z.guideXML.Channels = append(z.guideXML.Channels, channel)
	}
	return nil
}

func (z *Zap) BuildChannelXML(channelData map[string]interface{}) (Channel, error) {
	channel := Channel{
		ID: channelData["channelId"].(string),
		DisplayName: []string{
			fmt.Sprintf("%s %s", channelData["channelNo"], channelData["callSign"]),
			channelData["channelNo"].(string),
			channelData["callSign"].(string),
			strings.Title(channelData["affiliateName"].(string)),
		},
	}

	thumbnail := channelData["thumbnail"].(string)
	thumbnailURL := "http://" + strings.TrimLeft(strings.Split(thumbnail, "?")[0], "/")
	channel.Icon = &Icon{Src: thumbnailURL}

	return channel, nil
}

func (z *Zap) AddEventsToGuide(data map[string]interface{}) error {
	channels, ok := data["channels"].([]interface{})
	if !ok {
		return fmt.Errorf("channels not found in data")
	}
	for _, c := range channels {
		channelData := c.(map[string]interface{})
		channelID := channelData["channelId"].(string)
		events, ok := channelData["events"].([]interface{})
		if !ok {
			continue
		}
		for _, e := range events {
			eventData := e.(map[string]interface{})
			programme, err := z.BuildEventXML(eventData, channelID)
			if err != nil {
				return err
			}
			z.guideXML.Programmes = append(z.guideXML.Programmes, programme)
		}
	}
	return nil
}

// safeGetString safely extracts a string from a map
func safeGetString(m map[string]interface{}, key string) (string, bool) {
	val, exists := m[key]
	if !exists {
		return "", false
	}
	str, ok := val.(string)
	return str, ok
}

// safeGetMap safely extracts a nested map from a map
func safeGetMap(m map[string]interface{}, key string) (map[string]interface{}, bool) {
	val, exists := m[key]
	if !exists {
		return nil, false
	}
	subMap, ok := val.(map[string]interface{})
	return subMap, ok
}

// newProgramme creates a new Programme with required fields
func newProgramme(channelID string) Programme {
	return Programme{
		Channel: channelID,
	}
}

func (z *Zap) BuildEventXML(eventData map[string]interface{}, channelID string) (Programme, error) {
	if eventData == nil {
		return Programme{}, fmt.Errorf("eventData is nil")
	}

	programme := newProgramme(channelID)

	// Handle start and end times
	startTime, ok := safeGetString(eventData, "startTime")
	if !ok {
		return Programme{}, fmt.Errorf("invalid or missing startTime")
	}
	programme.Start = z.BuildXMLDate(startTime)

	endTime, ok := safeGetString(eventData, "endTime")
	if !ok {
		return Programme{}, fmt.Errorf("invalid or missing endTime")
	}
	programme.Stop = z.BuildXMLDate(endTime)

	// Extract program data
	programData, ok := safeGetMap(eventData, "program")
	if !ok {
		return Programme{}, fmt.Errorf("invalid or missing program data")
	}

	// Set title
	title, ok := safeGetString(programData, "title")
	if !ok {
		return Programme{}, fmt.Errorf("invalid or missing title")
	}
	programme.Title = []Title{{Lang: z.lang, Text: title}}

	// Set episode title if available
	if episodeTitle, ok := safeGetString(programData, "episodeTitle"); ok && episodeTitle != "" {
		programme.SubTitle = &SubTitle{Lang: z.lang, Text: episodeTitle}
	}

	// Set description
	shortDesc, ok := safeGetString(programData, "shortDesc")
	if !ok || shortDesc == "" {
		shortDesc = "Unavailable"
	}
	programme.Desc = &Desc{Lang: z.lang, Text: shortDesc}

	return programme, nil
}

func (z *Zap) BuildXMLDate(inTime string) string {
	output := strings.ReplaceAll(inTime, "-", "")
	output = strings.ReplaceAll(output, "T", "")
	output = strings.ReplaceAll(output, ":", "")
	output = strings.Replace(output, "Z", " +0000", 1)
	return output
}

func (z *Zap) WriteGuide() error {
	outputFile, err := os.Create(z.outputFile)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	encoder := xml.NewEncoder(outputFile)
	encoder.Indent("", "  ")
	if err := encoder.Encode(z.guideXML); err != nil {
		return err
	}

	return nil
}

func (z *Zap) CopyHistorical() error {
	timestampStr := time.Now().Format(".20060102150405") + ".xmltv"
	histGuideFile := strings.TrimSuffix(z.outputFile, ".xmltv") + timestampStr
	input, err := ioutil.ReadFile(z.outputFile)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(histGuideFile, input, 0644)
}

func (z *Zap) CleanHistorical() error {
	outputFilePath, err := filepath.Abs(z.outputFile)
	if err != nil {
		return err
	}
	outputDir := filepath.Dir(outputFilePath)
	files, err := ioutil.ReadDir(outputDir)
	if err != nil {
		return err
	}

	histGuideDays := z.config.Section("prefs").Key("historicalGuideDays").MustInt(0)
	cutoffTime := time.Now().Add(-time.Duration(histGuideDays) * 24 * time.Hour)

	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".xmltv") {
			filePath := filepath.Join(outputDir, file.Name())
			fileInfo, err := os.Stat(filePath)
			if err != nil {
				continue
			}
			if fileInfo.ModTime().Before(cutoffTime) {
				os.Remove(filePath)
			}
		}
	}
	return nil
}

func parseFlags() (configFile, guideFile, language string, findID bool) {
	pflag.StringVarP(&configFile, "configfile", "c", "./zap2itconfig.ini", "Path to config file")
	pflag.StringVarP(&guideFile, "outputfile", "o", "xmlguide.xmltv", "Path to output file")
	pflag.StringVarP(&language, "language", "l", "en", "Language")
	pflag.BoolVarP(&findID, "findid", "f", false, "Find Headendid / lineupid")
	pflag.Parse()
	return
}

func main() {
	configFile, guideFile, language, findID := parseFlags()

	guide, err := GuideScrape(configFile, guideFile)
	if err != nil {
		log.Fatalf("Failed to initialize guide: %v", err)
	}

	guide.lang = language

	if findID {
		if err := guide.FindID(); err != nil {
			log.Fatalf("Failed to find ID: %v", err)
		}
		os.Exit(0)
	}

	if err := guide.BuildGuide(); err != nil {
		log.Fatalf("Failed to build guide: %v", err)
	}
}
