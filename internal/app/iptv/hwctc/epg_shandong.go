package hwctc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iptv/internal/app/iptv"
	"net/http"
	"net/url"
	"time"
)

const (
	dateFormat       = "20060102"
	shandongTimeFmt = "15:04"
)

type shandongProgramResponse struct {
	Data []struct {
		ProgName    string `json:"progName"`
		StartTime   string `json:"startTime"`
		EndTime     string `json:"endTime"`
		SubProgName string `json:"subProgName"`
		ProgID      string `json:"progId"`
	} `json:"data"`
}

// getSdIptvChannelProgramList 山东专用节目单获取方法
func (c *Client) getSdIptvChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	datePrograms := make([]iptv.DateProgram, 0)
	now := time.Now()

	// 根据epg.go中定义的maxBackDay常量计算日期范围
	for offset := -maxBackDay + 1; offset <= 0; offset++ { // 生成7天回看
		targetDate := now.AddDate(0, 0, offset)
		dateStr := targetDate.Format(dateFormat)

		programs, err := c.getShandongDateProgram(ctx, token, channel, dateStr)
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				continue
			}
			c.logger.Warn(fmt.Sprintf("[山东EPG][%s] 获取失败 日期:%s 错误:%v",
				channel.ChannelName, dateStr, err))
			continue
		}

		datePrograms = append(datePrograms, iptv.DateProgram{
			Date:        targetDate,
			ProgramList: programs,
		})
	}

	return &iptv.ChannelProgramList{
		ChannelId:       channel.ChannelID,
		ChannelName:     channel.ChannelName,
		DateProgramList: datePrograms,
	}, nil
}

// getShandongDateProgram 获取指定日期的节目单
func (c *Client) getShandongDateProgram(ctx context.Context, token *Token, channel *iptv.Channel, dateStr string) ([]iptv.Program, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", 
		fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgList.jsp", c.host), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置山东接口特有参数
	q := req.URL.Query()
	q.Add("CHANNELID", channel.ChannelID)
	q.Add("date", dateStr)
	req.URL.RawQuery = q.Encode()

	// 设置山东特有请求头
	c.setShandongHeaders(req)
	
	// 设置认证Cookies
	c.setShandongCookies(req, token, channel)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrEPGApiNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("异常状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return c.parseShandongPrograms(body, dateStr)
}

// parseShandongPrograms 解析山东节目单响应
func (c *Client) parseShandongPrograms(data []byte, dateStr string) ([]iptv.Program, error) {
	var resp shandongProgramResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, ErrChProgListIsEmpty
	}

	baseDate, err := time.ParseInLocation(dateFormat, dateStr, time.Local)
	if err != nil {
		return nil, fmt.Errorf("日期解析失败: %s", dateStr)
	}

	programs := make([]iptv.Program, 0, len(resp.Data))
	for _, item := range resp.Data {
		start, err := parseShandongTime(baseDate, item.StartTime)
		if err != nil {
			c.logger.Warn(fmt.Sprintf("时间解析跳过[开始时间]: %s 错误: %v", item.StartTime, err))
			continue
		}

		end, err := parseShandongTime(baseDate, item.EndTime)
		if err != nil {
			c.logger.Warn(fmt.Sprintf("时间解析跳过[结束时间]: %s 错误: %v", item.EndTime, err))
			continue
		}

		// 处理跨天节目
		if end.Before(start) {
			end = end.AddDate(0, 0, 1)
		}

		programs = append(programs, iptv.Program{
			ProgramName:     item.ProgName,
			BeginTimeFormat: start.Format("20060102150405"),
			EndTimeFormat:   end.Format("20060102150405"),
			StartTime:       start.Format("15:04"),
			EndTime:         end.Format("15:04"),
		})
	}

	return programs, nil
}

// parseShandongTime 解析山东时间格式
func parseShandongTime(base time.Time, t string) (time.Time, error) {
	// 清理带秒的时间格式（如"00:12:00" -> "00:12"）
	if len(t) > 5 {
		t = t[:5]
	}
	
	parsed, err := time.ParseInLocation(shandongTimeFmt, t, base.Location())
	if err != nil {
		return time.Time{}, err
	}
	
	return time.Date(
		base.Year(),
		base.Month(),
		base.Day(),
		parsed.Hour(),
		parsed.Minute(),
		0, 0, base.Location(),
	), nil
}

// setShandongHeaders 设置山东特有请求头
func (c *Client) setShandongHeaders(req *http.Request) {
	headers := map[string]string{
		"User-Agent":       "Mozilla/5.0 (SMART-TV; Linux; Tizen 5.5) AppleWebKit/537.36",
		"Accept":           "application/json",
		"Referer":          fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/", c.host),
		"X-Requested-With": "com.hisense.iptv",
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

// setShandongCookies 设置山东认证Cookies
func (c *Client) setShandongCookies(req *http.Request, token *Token, channel *iptv.Channel) {
	cookies := []*http.Cookie{
		{Name: "JSESSIONID", Value: token.JSESSIONID},
		{Name: "STARV_TIMESHFTCID", Value: channel.ChannelID},
		{Name: "STARV_TIMESHFTCNAME", Value: url.QueryEscape(channel.ChannelName)},
		{Name: "maidianFlag", Value: "1"},
		{Name: "navNameFocus", Value: "3"},
		{Name: "channelTip", Value: "1"},
	}
	
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
}