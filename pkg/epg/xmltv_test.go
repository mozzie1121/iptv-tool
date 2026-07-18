package epg

import (
	"strings"
	"testing"
	"time"
)

func TestParseXMLTV_Basic(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<tv generator-info-name="test">
  <channel id="ch1">
    <display-name>CCTV-1</display-name>
  </channel>
  <channel id="ch2">
    <display-name lang="en">Channel 2</display-name>
    <display-name lang="zh">频道2</display-name>
  </channel>
  <programme start="20260312200000 +0800" stop="20260312210000 +0800" channel="ch1">
    <title>新闻联播</title>
    <desc>每日新闻</desc>
  </programme>
  <programme start="20260312210000 +0800" stop="20260312220000 +0800" channel="ch2">
    <title>电视剧</title>
  </programme>
</tv>`

	programs, err := ParseXMLTV(xmlData)
	if err != nil {
		t.Fatalf("ParseXMLTV returned error: %v", err)
	}

	if len(programs) != 2 {
		t.Fatalf("expected 2 programs, got %d", len(programs))
	}

	// Verify first program
	p := programs[0]
	if p.Channel != "ch1" {
		t.Errorf("program[0].Channel = %q, want %q", p.Channel, "ch1")
	}
	if p.ChannelName != "CCTV-1" {
		t.Errorf("program[0].ChannelName = %q, want %q", p.ChannelName, "CCTV-1")
	}
	if p.Title != "新闻联播" {
		t.Errorf("program[0].Title = %q, want %q", p.Title, "新闻联播")
	}
	if p.Desc != "每日新闻" {
		t.Errorf("program[0].Desc = %q, want %q", p.Desc, "每日新闻")
	}

	// Verify second program uses zh display-name
	p2 := programs[1]
	if p2.ChannelName != "频道2" {
		t.Errorf("program[1].ChannelName = %q, want %q (zh preferred)", p2.ChannelName, "频道2")
	}
	if p2.Desc != "" {
		t.Errorf("program[1].Desc = %q, want empty", p2.Desc)
	}
}

func TestParseXMLTV_ZhLangVariants(t *testing.T) {
	tests := []struct {
		name     string
		lang     string
		wantName string
	}{
		{"zh", "zh", "中文名称"},
		{"zh-CN", "zh-CN", "中文名称"},
		{"zh-TW", "zh-TW", "中文名称"},
		{"zh_CN", "zh_CN", "中文名称"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<tv>
  <channel id="c1">
    <display-name lang="en">English Name</display-name>
    <display-name lang="` + tt.lang + `">中文名称</display-name>
  </channel>
  <programme start="20260312200000 +0800" stop="20260312210000 +0800" channel="c1">
    <title>Test</title>
  </programme>
</tv>`
			programs, err := ParseXMLTV(xmlData)
			if err != nil {
				t.Fatalf("ParseXMLTV returned error: %v", err)
			}
			if len(programs) != 1 {
				t.Fatalf("expected 1 program, got %d", len(programs))
			}
			if programs[0].ChannelName != tt.wantName {
				t.Errorf("ChannelName = %q, want %q", programs[0].ChannelName, tt.wantName)
			}
		})
	}
}

func TestParseXMLTV_FallbackDisplayName(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<tv>
  <channel id="c1">
    <display-name lang="en">English Only</display-name>
    <display-name lang="fr">Français</display-name>
  </channel>
  <programme start="20260312200000 +0800" stop="20260312210000 +0800" channel="c1">
    <title>Test</title>
  </programme>
</tv>`

	programs, err := ParseXMLTV(xmlData)
	if err != nil {
		t.Fatalf("ParseXMLTV returned error: %v", err)
	}
	if programs[0].ChannelName != "English Only" {
		t.Errorf("ChannelName = %q, want %q (fallback to first)", programs[0].ChannelName, "English Only")
	}
}

func TestParseXMLTV_EmptyInput(t *testing.T) {
	programs, err := ParseXMLTV("")
	if err != nil {
		t.Fatalf("ParseXMLTV on empty input returned error: %v", err)
	}
	if len(programs) != 0 {
		t.Errorf("expected 0 programs from empty input, got %d", len(programs))
	}
}

func TestParseXMLTV_NoProgrammes(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<tv>
  <channel id="c1">
    <display-name>CCTV-1</display-name>
  </channel>
</tv>`

	programs, err := ParseXMLTV(xmlData)
	if err != nil {
		t.Fatalf("ParseXMLTV returned error: %v", err)
	}
	if len(programs) != 0 {
		t.Errorf("expected 0 programs, got %d", len(programs))
	}
}

func TestParseXMLTV_SkipsMalformedTimes(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<tv>
  <channel id="c1">
    <display-name>Test</display-name>
  </channel>
  <programme start="invalid" stop="20260312210000 +0800" channel="c1">
    <title>Bad Start</title>
  </programme>
  <programme start="20260312200000 +0800" stop="invalid" channel="c1">
    <title>Bad Stop</title>
  </programme>
  <programme start="20260312200000 +0800" stop="20260312210000 +0800" channel="c1">
    <title>Good</title>
  </programme>
</tv>`

	programs, err := ParseXMLTV(xmlData)
	if err != nil {
		t.Fatalf("ParseXMLTV returned error: %v", err)
	}
	if len(programs) != 1 {
		t.Fatalf("expected 1 valid program (2 skipped), got %d", len(programs))
	}
	if programs[0].Title != "Good" {
		t.Errorf("program.Title = %q, want %q", programs[0].Title, "Good")
	}
}

func TestParseXMLTVTime(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"with timezone", "20260312200000 +0800", false},
		{"without timezone", "20260312200000", false},
		{"with spaces", "  20260312200000 +0800  ", false},
		{"invalid", "not-a-time", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseXMLTVTime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseXMLTVTime(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && result.IsZero() {
				t.Errorf("parseXMLTVTime(%q) returned zero time", tt.input)
			}
		})
	}
}

func TestParseXMLTV_StreamingFromReader(t *testing.T) {
	// Verify streaming works correctly via parseXMLTVFromReader with a Reader
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<tv>
  <channel id="c1">
    <display-name>Channel 1</display-name>
  </channel>
  <programme start="20260312200000 +0800" stop="20260312210000 +0800" channel="c1">
    <title>Program 1</title>
  </programme>
</tv>`

	programs, err := parseXMLTVFromReader(strings.NewReader(xmlData))
	if err != nil {
		t.Fatalf("parseXMLTVFromReader returned error: %v", err)
	}
	if len(programs) != 1 {
		t.Fatalf("expected 1 program, got %d", len(programs))
	}

	p := programs[0]
	expectedStart := time.Date(2026, 3, 12, 20, 0, 0, 0, time.FixedZone("", 8*3600))
	if !p.StartTime.Equal(expectedStart) {
		t.Errorf("StartTime = %v, want %v", p.StartTime, expectedStart)
	}
}
