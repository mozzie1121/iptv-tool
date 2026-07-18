package epg

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// XMLTV structures for parsing standard XMLTV format

type TV struct {
	XMLName    xml.Name     `xml:"tv"`
	Channels   []XMLChannel `xml:"channel"`
	Programmes []Programme  `xml:"programme"`
}

type XMLChannel struct {
	ID          string        `xml:"id,attr"`
	DisplayName []DisplayName `xml:"display-name"`
	Icon        *Icon         `xml:"icon,omitempty"`
}

type DisplayName struct {
	Value string `xml:",chardata"`
	Lang  string `xml:"lang,attr,omitempty"`
}

type Icon struct {
	Src string `xml:"src,attr"`
}

type Programme struct {
	Start   string  `xml:"start,attr"`
	Stop    string  `xml:"stop,attr"`
	Channel string  `xml:"channel,attr"`
	Title   []Title `xml:"title"`
	Desc    []Desc  `xml:"desc,omitempty"`
}

type Title struct {
	Value string `xml:",chardata"`
	Lang  string `xml:"lang,attr,omitempty"`
}

type Desc struct {
	Value string `xml:",chardata"`
	Lang  string `xml:"lang,attr,omitempty"`
}

// Program represents a unified EPG program entry used internally
type Program struct {
	Channel     string // Channel ID (XMLTV channel id / IPTV ChannelID)
	ChannelName string // Channel display name
	Title       string
	Desc        string
	StartTime   time.Time
	EndTime     time.Time
}

// ParseXMLTV parses XMLTV format content (plain XML string) and returns a list of programs.
func ParseXMLTV(content string) ([]Program, error) {
	return parseXMLTVFromReader(strings.NewReader(content))
}

// parseXMLTVFromReader parses XMLTV from an io.Reader using SAX-style streaming.
// Instead of loading the entire XML tree into memory, it processes one <channel>
// or <programme> element at a time via decoder.Token() + decoder.DecodeElement(),
// significantly reducing peak memory usage for large XMLTV files.
func parseXMLTVFromReader(r io.Reader) ([]Program, error) {
	decoder := xml.NewDecoder(r)

	// Build channel ID -> display name map (populated as we encounter <channel> elements)
	channelNameMap := make(map[string]string)
	var programs []Program

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to parse XMLTV token: %w", err)
		}

		se, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		switch se.Name.Local {
		case "channel":
			// Decode only this single <channel> element
			var ch XMLChannel
			if err := decoder.DecodeElement(&ch, &se); err != nil {
				continue // skip malformed channel
			}
			// Prefer lang="zh" display-name, fallback to first available
			name := ""
			if len(ch.DisplayName) > 0 {
				for _, dn := range ch.DisplayName {
					lang := strings.ToLower(dn.Lang)
					if lang == "zh" || strings.HasPrefix(lang, "zh-") || strings.HasPrefix(lang, "zh_") {
						name = dn.Value
						break
					}
				}
				if name == "" {
					name = ch.DisplayName[0].Value
				}
			}
			channelNameMap[ch.ID] = name

		case "programme":
			// Decode only this single <programme> element
			var prog Programme
			if err := decoder.DecodeElement(&prog, &se); err != nil {
				continue // skip malformed programme
			}
			startTime, err := parseXMLTVTime(prog.Start)
			if err != nil {
				continue
			}
			endTime, err := parseXMLTVTime(prog.Stop)
			if err != nil {
				continue
			}

			title := ""
			if len(prog.Title) > 0 {
				title = prog.Title[0].Value
			}
			desc := ""
			if len(prog.Desc) > 0 {
				desc = prog.Desc[0].Value
			}

			programs = append(programs, Program{
				Channel:     prog.Channel,
				ChannelName: channelNameMap[prog.Channel],
				Title:       title,
				Desc:        desc,
				StartTime:   startTime,
				EndTime:     endTime,
			})
		}
	}

	return programs, nil
}

// FetchAndParseXMLTV fetches XMLTV from a URL, automatically detecting and handling gzip compression.
// headersJSON is an optional JSON string containing custom HTTP headers (e.g., {"User-Agent": "...", "Authorization": "..."}).
func FetchAndParseXMLTV(url string, headersJSON string) ([]Program, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", url, err)
	}

	applyCustomHeaders(req, headersJSON)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch XMLTV from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch XMLTV: HTTP %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body

	// Detect gzip compression by Content-Encoding header or Content-Type
	contentEncoding := resp.Header.Get("Content-Encoding")
	contentType := resp.Header.Get("Content-Type")

	if strings.Contains(contentEncoding, "gzip") ||
		strings.Contains(contentType, "gzip") ||
		strings.HasSuffix(strings.ToLower(url), ".gz") ||
		strings.HasSuffix(strings.ToLower(url), ".xml.gz") {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			// Maybe not actually gzip, try reading as plain XML by re-fetching
			return retryAsPlainXMLTV(url, headersJSON)
		}
		defer gzReader.Close()
		reader = gzReader
	} else {
		// Read first 2 bytes to detect gzip magic number (0x1f 0x8b)
		buf := make([]byte, 2)
		n, err := resp.Body.Read(buf)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		if n >= 2 && buf[0] == 0x1f && buf[1] == 0x8b {
			// It's gzip compressed even though headers didn't say so
			combined := io.MultiReader(bytes.NewReader(buf[:n]), resp.Body)
			gzReader, err := gzip.NewReader(combined)
			if err != nil {
				return nil, fmt.Errorf("failed to create gzip reader: %w", err)
			}
			defer gzReader.Close()
			reader = gzReader
		} else {
			// Plain XML, prepend the bytes we already read
			reader = io.MultiReader(bytes.NewReader(buf[:n]), resp.Body)
		}
	}

	return parseXMLTVFromReader(reader)
}

// retryAsPlainXMLTV retries fetching URL as plain XML (fallback when gzip detection is wrong)
func retryAsPlainXMLTV(url string, headersJSON string) ([]Program, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	applyCustomHeaders(req, headersJSON)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return parseXMLTVFromReader(resp.Body)
}

// applyCustomHeaders parses a JSON string of headers and applies them to the request.
func applyCustomHeaders(req *http.Request, headersJSON string) {
	if headersJSON == "" {
		return
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(headersJSON), &headers); err == nil {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	} else {
		slog.Warn("Failed to parse custom headers for XMLTV fetch", "error", err)
	}
}

// parseXMLTVTime parses XMLTV time format: "20060102150405 +0800" or "20060102150405"
func parseXMLTVTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)

	// Try with timezone offset first
	t, err := time.Parse("20060102150405 -0700", s)
	if err == nil {
		return t, nil
	}

	// Try without timezone (assume local)
	t, err = time.ParseInLocation("20060102150405", s, time.Local)
	if err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("failed to parse XMLTV time: %s", s)
}

// GenerateXMLTV generates XMLTV format XML string from a list of programs.
// channelMap maps channel ID to display name.
func GenerateXMLTV(programs []Program, channelMap map[string]string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString("\n")
	sb.WriteString(`<tv generator-info-name="iptv-tool-v2">`)
	sb.WriteString("\n")

	// Write channel definitions
	for id, name := range channelMap {
		sb.WriteString(fmt.Sprintf(`  <channel id="%s">`, xmlEscape(id)))
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf(`    <display-name>%s</display-name>`, xmlEscape(name)))
		sb.WriteString("\n")
		sb.WriteString("  </channel>\n")
	}

	// Write programmes
	for _, prog := range programs {
		start := prog.StartTime.Format("20060102150405 -0700")
		stop := prog.EndTime.Format("20060102150405 -0700")
		sb.WriteString(fmt.Sprintf(`  <programme start="%s" stop="%s" channel="%s">`,
			start, stop, xmlEscape(prog.Channel)))
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf(`    <title>%s</title>`, xmlEscape(prog.Title)))
		sb.WriteString("\n")
		if prog.Desc != "" {
			sb.WriteString(fmt.Sprintf(`    <desc>%s</desc>`, xmlEscape(prog.Desc)))
			sb.WriteString("\n")
		}
		sb.WriteString("  </programme>\n")
	}

	sb.WriteString("</tv>\n")
	return sb.String()
}

// xmlEscape escapes special XML characters
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
