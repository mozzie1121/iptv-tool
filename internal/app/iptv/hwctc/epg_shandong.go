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
	"strconv"
	"time"
)

const (
	minIndex = -7  // 最早7天前
	maxIndex = 4   // 最晚4天后
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

// getSdIptvChannelProgramList 获取山东频道节目单
func (c *Client) getSdIptvChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	datePrograms := make([]iptv.DateProgram, 0)

	// 遍历index范围 [-7, 4]
	for index := minIndex; index <= maxIndex; index++ {
		// 计算实际日期
		targetDate := time.Now().AddDate(0, 0, index)
		dateStr := targetDate.Format("20060102")

		programs, err := c.getShandongIndexProgram(ctx, token, channel, index)
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				continue
			}
			c.logger.Warn(fmt.Sprintf("[山东EPG][%s] 获取失败 index:%d 日期:%s 错误:%v",
				channel.ChannelName, index, dateStr, err))
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

// getShandongIndexProgram 通过index获取节目单
func (c *Client) getShandongIndexProgram(ctx context.Context, token *Token, channel *iptv.Channel, index int) ([]iptv.Program, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", 
		fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgList.jsp", c.host), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置查询参数
	q := req.URL.Query()
	q.Add("CHANNELID", channel.ChannelID)
	q.Add("index", strconv.Itoa(index)) // 关键修改：使用index参数
	req.URL.RawQuery = q.Encode()

	// 设置请求头和Cookies（保持不变）
	c.setShandongHeaders(req)
	c.setShandongCookies(req, token, channel)

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 处理响应状态码
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrEPGApiNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("异常状态码: %d", resp.StatusCode)
	}

	// 解析响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return c.parseShandongPrograms(body, index)
}

// parseShandongPrograms 解析节目单（新增index参数处理跨天）
func (c *Client) parseShandongPrograms(data []byte, index int) ([]iptv.Program, error) {
	var resp shandongProgramResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, ErrChProgListIsEmpty
	}

	// 根据index计算基准日期
	baseDate := time.Now().AddDate(0, 0, index)
	
	programs := make([]iptv.Program, 0, len(resp.Data))
	for _, item := range resp.Data {
		// 解析时间（自动处理跨天）
		start, end, err := parseShandongTimes(baseDate, item.StartTime, item.EndTime)
		if err != nil {
			c.logger.Warn(fmt.Sprintf("时间解析跳过: 开始[%s] 结束[%s] 错误:%v",
				item.StartTime, item.EndTime, err))
			continue
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

// parseShandongTimes 智能处理跨天时间
func parseShandongTimes(baseDate time.Time, startStr, endStr string) (time.Time, time.Time, error) {
	// 清理时间格式（处理类似"00:12:00"的情况）
	if len(startStr) > 5 {
		startStr = startStr[:5]
	}
	if len(endStr) > 5 {
		endStr = endStr[:5]
	}

	// 解析基础时间
	startTime, err := time.ParseInLocation("15:04", startStr, baseDate.Location())
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	endTime, err := time.ParseInLocation("15:04", endStr, baseDate.Location())
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	// 创建完整时间对象
	start := time.Date(
		baseDate.Year(), baseDate.Month(), baseDate.Day(),
		startTime.Hour(), startTime.Minute(), 0, 0, baseDate.Location(),
	)
	end := time.Date(
		baseDate.Year(), baseDate.Month(), baseDate.Day(),
		endTime.Hour(), endTime.Minute(), 0, 0, baseDate.Location(),
	)

	// 处理跨天节目（例如23:00-01:30）
	if end.Before(start) {
		end = end.AddDate(0, 0, 1)
	}

	return start, end, nil
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