package hwctc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"iptv/internal/app/iptv"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// GetAllChannelList 获取所有频道列表
func (c *Client) GetAllChannelList(ctx context.Context) ([]iptv.Channel, error) {
	// 请求认证的Token
	token, err := c.requestToken(ctx)
	if err != nil {
		return nil, err
	}

	// 组装请求数据
	data := map[string]string{
		"conntype":  c.config.Conntype,
		"UserToken": token.UserToken,
		"tempKey":   "",
		"stbid":     token.Stbid,
		"SupportHD": "1",
		"UserID":    c.config.UserID,
		"Lang":      "1",
	}
	body := url.Values{}
	for k, v := range data {
		body.Add(k, v)
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://%s/EPG/jsp/getchannellistHWCU.jsp", c.host), strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}

	// 设置请求头
	c.setCommonHeaders(req)
	req.Header.Set("Referer", fmt.Sprintf("http://%s/EPG/jsp/ValidAuthenticationHWCU.jsp", c.host))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// 设置Cookie
	req.AddCookie(&http.Cookie{
		Name:  "JSESSIONID",
		Value: token.JSESSIONID,
	})

	// 执行请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status code: %d", resp.StatusCode)
	}

	// 解析响应内容
	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	err = writeToFile("channel.jsp", string(result))
	if err != nil {
		return nil, err
	}
	chRegex := regexp.MustCompile("ChannelID=\"(.+?)\",ChannelName=\"(.+?)\",UserChannelID=\"(.+?)\",ChannelURL=\"(.+?)\",TimeShift=\"(.+?)\",TimeShiftLength=\"(\\d+?)\".+?,TimeShiftURL=\"(.+?)\"")
	matchesList := chRegex.FindAllSubmatch(result, -1)
	c.logger.Info("matchesList cnt: " + strconv.Itoa(len(matchesList)))
	if matchesList == nil {
		return nil, fmt.Errorf("failed to extract channel list")
	}

	channels := make([]iptv.Channel, 0, len(matchesList))
	for _, matches := range matchesList {
		if len(matches) != 8 {
			continue
		}

		channelName := string(matches[2])
		// 过滤掉特殊频道
		if c.chExcludeRule != nil && c.chExcludeRule.MatchString(channelName) {
			c.logger.Warn("This is not a normal channel, skip it.", zap.String("channelName", channelName))
			continue
		}

		// channelURL类型转换
		// channelURL可能同时返回组播和单播多个地址（通过|分割）
		//channelURL := string(matches[4])
		channelURL := strings.TrimPrefix(string(matches[4]), "igmp://")

		// TimeShiftLength类型转换
		timeShiftLength, err := strconv.ParseInt(string(matches[6]), 10, 64)
		if err != nil {
			c.logger.Warn("The timeShiftLength of this channel is illegal. Use the default value: 0.", zap.String("channelName", channelName), zap.String("timeShiftLength", string(matches[6])))
			timeShiftLength = 0
		}

		// 解析时移地址
		timeShiftURL := string(matches[7])

		// 自动识别频道的分类
		groupName := iptv.GetChannelGroupName(c.chGroupRulesList, channelName)

		// 识别频道台标logo
		logoName := iptv.GetChannelLogoName(c.chLogoRuleList, channelName)

		channels = append(channels, iptv.Channel{
			ChannelID:       string(matches[1]),
			ChannelName:     channelName,
			UserChannelID:   string(matches[3]),
			ChannelURL:      channelURL,
			TimeShift:       string(matches[5]),
			TimeShiftLength: time.Duration(timeShiftLength) * time.Minute,
			TimeShiftURL:    timeShiftURL,
			GroupName:       groupName,
			LogoName:        logoName,
		})
	}
	return channels, nil
}

func writeToFile(filename, content string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	if _, err := writer.WriteString(content); err != nil {
		return fmt.Errorf("写入内容失败: %w", err)
	}

	return nil
}
