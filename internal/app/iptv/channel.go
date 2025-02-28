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
	ChannelID       string        `json:"channelID"`       // 频道ID
	ChannelName     string        `json:"channelName"`     // 频道名称
	UserChannelID   string        `json:"userChannelID"`   // 频道号
	ChannelURLs     []url.URL     `json:"channelURLs"`     // 频道URL列表
	TimeShift       string        `json:"timeShift"`       // 时移类型
	TimeShiftLength time.Duration `json:"timeShiftLength"` // 支持的时移长度
	TimeShiftURL    *url.URL      `json:"timeShiftURL"`    // 时移地址（回放地址）

	GroupName string `json:"groupName"` // 程序识别的频道分类
	LogoName  string `json:"logoName"`  // 频道台标名称
}

// ToM3UFormat 转换为M3U格式内容
func ToM3UFormat(channels []Channel, udpxyURL, catchupSource string, multicastFirst bool, logoBaseUrl string) (string, error) {
	if len(channels) == 0 {
		return "", errors.New("no channels found")
	}

	catchupSource = strings.TrimLeft(catchupSource, "?&")

	currDir, err := util.GetCurrentAbPathByExecutable()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	for _, channel := range channels {
		// 根据指定条件，获取频道URL地址
		channelURLStr, isMulticastCh, err := getChannelURLStr(channel.ChannelURLs, udpxyURL, multicastFirst)
		if err != nil {
			return "", err
		}

		var m3uLineSb strings.Builder

		// 设置频道ID和序号
		m3uLineSb.WriteString(fmt.Sprintf("#EXTINF:-1 tvg-id=\"%s\" tvg-chno=\"%s\"",
			channel.ChannelID, channel.UserChannelID))
		// 设置频道的台标URL
		if logoBaseUrl != "" && channel.LogoName != "" {
			logoFile := channel.LogoName + ".png"
			if _, err = os.Stat(filepath.Join(currDir, logoDirName, logoFile)); !os.IsNotExist(err) {
				if logoUrl, err := url.JoinPath(logoBaseUrl, logoFile); err == nil {
					m3uLineSb.WriteString(fmt.Sprintf(" tvg-logo=\"%s\"",
						logoUrl))
				}
			}
		}
		// 设置频道回看参数
		if channel.TimeShift == "1" && channel.TimeShiftLength > 0 && channel.TimeShiftURL != nil {
			var chCatchup, chCatchupSource string
			if isMulticastCh {
				chCatchup = "default"
				chCatchupSource = channel.TimeShiftURL.String()
				if channel.TimeShiftURL.RawQuery != "" {
					chCatchupSource += "&" + catchupSource
				} else {
					chCatchupSource += "?" + catchupSource
				}
			} else {
				chCatchup = "append"
				chCatchupSource = "?" + catchupSource
			}

			m3uLineSb.WriteString(fmt.Sprintf(" catchup=\"%s\" catchup-source=\"%s\" catchup-days=\"%d\"",
				chCatchup, chCatchupSource, int64(channel.TimeShiftLength.Hours()/24)))
		}
		// 设置频道分组和名称
		m3uLineSb.WriteString(fmt.Sprintf(" group-title=\"%s\",%s\n%s\n",
			channel.GroupName, channel.ChannelName, channelURLStr))
		sb.WriteString(m3uLineSb.String())
	}
	return sb.String(), nil
}

// ToTxtFormat 转换为txt格式内容
func ToTxtFormat(channels []Channel, udpxyURL string, multicastFirst bool) (string, error) {
	if len(channels) == 0 {
		return "", errors.New("no channels found")
	}

	// 对频道列表，按分组名称进行分组
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
	// 为保证顺序，单独遍历分组名称的slices
	for _, groupName := range groupNames {
		groupChannels := groupChannelMap[groupName]

		// 输出分组信息
		groupLine := fmt.Sprintf("%s,#genre#\n", groupName)
		sb.WriteString(groupLine)

		// 输出频道信息
		for _, channel := range groupChannels {
			// 根据指定条件，获取频道URL地址
			channelURLStr, _, err := getChannelURLStr(channel.ChannelURLs, udpxyURL, multicastFirst)
			if err != nil {
				return "", err
			}

			txtLine := fmt.Sprintf("%s,%s\n",
				channel.ChannelName, channelURLStr)
			sb.WriteString(txtLine)
		}
	}
	return sb.String(), nil
}

// getChannelURLStr 根据指定条件，获取频道URL地址
func getChannelURLStr(channelURLs []url.URL, udpxyURL string, multicastFirst bool) (string, bool, error) {
	if len(channelURLs) == 0 {
		return "", false, errors.New("no channel urls found")
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

	isMulticastCh := channelURL.Scheme == SCHEME_IGMP
	if udpxyURL != "" && isMulticastCh {
		result, err := url.JoinPath(udpxyURL, fmt.Sprintf("/rtp/%s", channelURL.Host))
		return result, isMulticastCh, err
	} else {
		return channelURL.String(), isMulticastCh, nil
	}
}
