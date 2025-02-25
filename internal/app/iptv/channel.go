package iptv

import (
	"errors"
	"fmt"
	"iptv/internal/pkg/util"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const SCHEME_IGMP = "igmp"

// Channel 频道信息
type Channel struct {
	ChannelID       string        `json:"channelID"`       
	ChannelName     string        `json:"channelName"`     
	UserChannelID   string        `json:"userChannelID"`   
	ChannelURLs     []url.URL     `json:"channelURLs"`     
	TimeShift       string        `json:"timeShift"`       
	TimeShiftLength time.Duration `json:"timeShiftLength"` 
	TimeShiftURL    *url.URL      `json:"timeShiftURL"`    

	GroupName string `json:"groupName"` 
	LogoName  string `json:"logoName"`  
}

// ToM3UFormat 
func ToM3UFormat(
	channels []Channel, 
	udpxyURL, 
	catchupSource string, 
	catchUpMode string,  // 新增参数
	multicastFirst bool, 
	logoBaseUrl string,
) (string, error) {
	if len(channels) == 0 {
		return "", errors.New("no channels found")
	}

	currDir, err := util.GetCurrentAbPathByExecutable()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	for _, channel := range channels {
		channelURLStr, err := getChannelURLStr(channel.ChannelURLs, udpxyURL, multicastFirst)
		if err != nil {
			return "", err
		}

		var m3uLineSb strings.Builder
		m3uLineSb.WriteString(fmt.Sprintf("#EXTINF:-1 tvg-id=\"%s\" tvg-chno=\"%s\"",
			channel.ChannelID, channel.UserChannelID))

		// 台标处理
		if logoBaseUrl != "" && channel.LogoName != "" {
			logoFile := channel.LogoName + ".png"
			if _, err = os.Stat(filepath.Join(currDir, logoDirName, logoFile)); !os.IsNotExist(err) {
				if logoUrl, err := url.JoinPath(logoBaseUrl, logoFile); err == nil {
					m3uLineSb.WriteString(fmt.Sprintf(" tvg-logo=\"%s\"", logoUrl))
				}
			}
		}

		// 回看参数生成
		if channel.TimeShift == "1" && channel.TimeShiftLength > 0 && channel.TimeShiftURL != nil {
			baseURL := channel.TimeShiftURL.String()
			var sourceURL string

			// 新增模式切换逻辑
			switch catchUpMode {
			case "1": // append模式
				sourceURL = baseURL + catchupSource
			case "2": // flussonic模式
				sourceURL = fmt.Sprintf("%s?start=${start}&end=${end}&dvr=${duration}", baseURL)
			case "3": // xdomo模式
				sourceURL = fmt.Sprintf("%s?timeshift=${start}-${end}", baseURL)
			case "4": // custom模式
				sourceURL = fmt.Sprintf("%s?%s", baseURL, catchupSource)
			default:  // 0或其他值使用默认模式
				sourceURL = baseURL
			}

			m3uLineSb.WriteString(fmt.Sprintf(
				" catchup=\"%s\" catchup-source=\"%s\" catchup-days=\"%d\"",
				mapCatchupMode(catchUpMode), // 映射模式名称
				sourceURL,
				int64(channel.TimeShiftLength.Hours()/24),
			))
		}

		// 分组信息
		m3uLineSb.WriteString(fmt.Sprintf(" group-title=\"%s\",%s\n%s\n",
			channel.GroupName, channel.ChannelName, channelURLStr))
		sb.WriteString(m3uLineSb.String())
	}
	return sb.String(), nil
}

// 新增的私有映射函数
func mapCatchupMode(param string) string {
	switch param {
	case "1": return "append"
	case "2": return "flussonic"
	case "3": return "xdomo"
	case "4": return "custom"
	default: return "default"
	}
}

// ToTxtFormat
func ToTxtFormat(channels []Channel, udpxyURL string, multicastFirst bool) (string, error) {
	if len(channels) == 0 {
		return "", errors.New("no channels found")
	}

	groupNames := make([]string, 0)
	groupChannelMap := make(map[string][]Channel)
	for _, channel := range channels {
		groupChannels, ok := groupChannelMap[channel.GroupName]
		if !ok {
			groupNames = append(groupNames, channel.GroupName)
			groupChannelMap[channel.GroupName] = []Channel{channel}
			continue
		}

		groupChannels = append(groupChannels, channel)
		groupChannelMap[channel.GroupName] = groupChannels
	}

	var sb strings.Builder
	for _, groupName := range groupNames {
		groupChannels := groupChannelMap[groupName]
		groupLine := fmt.Sprintf("%s,#genre#\n", groupName)
		sb.WriteString(groupLine)

		for _, channel := range groupChannels {
			channelURLStr, err := getChannelURLStr(channel.ChannelURLs, udpxyURL, multicastFirst)
			if err != nil {
				return "", err
			}
			txtLine := fmt.Sprintf("%s,%s\n", channel.ChannelName, channelURLStr)
			sb.WriteString(txtLine)
		}
	}
	return sb.String(), nil
}

// getChannelURLStr 
func getChannelURLStr(channelURLs []url.URL, udpxyURL string, multicastFirst bool) (string, error) {
	if len(channelURLs) == 0 {
		return "", errors.New("no channel urls found")
	}

	var channelURL url.URL
	if len(channelURLs) == 1 {
		channelURL = channelURLs[0]
	} else {
		for _, channelURL = range channelURLs {
			if (multicastFirst && channelURL.Scheme == SCHEME_IGMP) ||
				(!multicastFirst && channelURL.Scheme != SCHEME_IGMP) {
				break
			}
		}
	}

	if udpxyURL != "" && channelURL.Scheme == SCHEME_IGMP {
		return url.JoinPath(udpxyURL, fmt.Sprintf("/rtp/%s", channelURL.Host))
	} else {
		return channelURL.String(), nil
	}
}