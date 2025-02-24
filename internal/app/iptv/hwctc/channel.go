package hwctc

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"iptv/internal/app/iptv"
	"net/http"
	"net/url"
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

	// 计算JSESSIONID的MD5
	hash := md5.Sum([]byte(token.JSESSIONID))
	// 转换为16进制字符串并转换为大写，即为tempKey
	tempKey := strings.ToUpper(hex.EncodeToString(hash[:]))

	// 组装请求数据
	data := map[string]string{
		"conntype":  c.config.Conntype,
		"UserToken": token.UserToken,
		"tempKey":   tempKey,
		"stbid":     token.Stbid,
		"SupportHD": "1",
		"UserID":    c.config.UserID,
		"Lang":      c.config.Lang,
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
	chRegex := regexp.MustCompile("ChannelID=\"(.+?)\",ChannelName=\"(.+?)\",UserChannelID=\"(.+?)\",ChannelURL=\"(.+?)\",TimeShift=\"(.+?)\",TimeShiftLength=\"(\\d+?)\".+?,TimeShiftURL=\"(.+?)\"")
	matchesList := chRegex.FindAllSubmatch(result, -1)
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
		channelURLStrList := strings.Split(string(matches[4]), "|")
		channelURLs := make([]url.URL, 0, len(channelURLStrList))
		for _, channelURLStr := range channelURLStrList {
			channelURL, err := url.Parse(channelURLStr)
			if err != nil {
				continue
			}

			channelURLs = append(channelURLs, *channelURL)
		}

		if len(channelURLs) == 0 {
			c.logger.Warn("The channelURL of this channel is illegal, skip it.", zap.String("channelName", channelName), zap.String("channelURL", string(matches[4])))
			continue
		}

		// TimeShiftLength类型转换
		timeShiftLength, err := strconv.ParseInt(string(matches[6]), 10, 64)
		if err != nil {
			c.logger.Warn("The timeShiftLength of this channel is illegal. Use the default value: 0.", zap.String("channelName", channelName), zap.String("timeShiftLength", string(matches[6])))
			timeShiftLength = 0
		}

		// 解析时移地址
		timeShiftURL, err := url.Parse(string(matches[7]))
		if err != nil {
			c.logger.Warn("The timeShiftURL of this channel is illegal. Use the default value: nil.", zap.String("channelName", channelName), zap.String("timeShiftURL", string(matches[7])))
		
		}
		//if timeShiftURL != nil {
			// 重置时移地址的查询参数
		//	timeShiftURL.RawQuery = ""
		//}

		// 自动识别频道的分类
		groupName := iptv.GetChannelGroupName(c.chGroupRulesList, channelName)

		// 识别频道台标logo
		logoName := iptv.GetChannelLogoName(c.chLogoRuleList, channelName)

		channels = append(channels, iptv.Channel{
			ChannelID:       string(matches[1]),
			ChannelName:     channelName,
			UserChannelID:   string(matches[3]),
			ChannelURLs:     channelURLs,
			TimeShift:       string(matches[5]),
			TimeShiftLength: time.Duration(timeShiftLength) * time.Minute,
			TimeShiftURL:    timeShiftURL,
			GroupName:       groupName,
			LogoName:        logoName,
		})
	}
	return channels, nil
}
