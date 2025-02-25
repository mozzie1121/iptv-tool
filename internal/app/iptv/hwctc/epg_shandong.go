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
		ProgID      string `json:"progId"` // 注意JSON标签匹配
	} `json:"data"`
}

// getSdIptvChannelProgramList 获取山东频道节目单
func (c *Client) getSdIptvChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	datePrograms := make([]iptv.DateProgram, 0)

	// 遍历index范围 [-7, 4]
	for index := minIndex; index <= maxIndex; index++ {
		programs, err := c.getShandongIndexProgram(ctx, token, channel, index)
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				continue
			}
			c.logger.Warn(fmt.Sprintf("[山东EPG][%s] 获取失败 index:%d 错误:%v",
				channel.ChannelName, index, err))
			continue
		}

		datePrograms = append(datePrograms, iptv.DateProgram{
			Date:        time.Now().AddDate(0, 0, index),
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
		fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgListByIndex.jsp", c.host), nil) // 修正URL路径
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置查询参数
	q := req.URL.Query()
	q.Add("CHANNELID", channel.ChannelID)
	q.Add("index", strconv.Itoa(index))
	req.URL.RawQuery = q.Encode()

	// 设置请求头
	req.Header.Set("User-Agent", "webkit;Resolution(PAL,720P,1080P)")
	req.Header.Set("X-Requested-With", "com.hisense.iptv")
	req.Header.Set("Referer", fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/chanMiniList.html", c.host))
	
	// 设置Cookies
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

	return c.parseShandongPrograms(body, index)
}

// parseShandongPrograms 解析节目单响应
func (c *Client) parseShandongPrograms(data []byte, index int) ([]iptv.Program, error) {
	var resp shandongProgramResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, ErrChProgListIsEmpty
	}

	baseDate := time.Now().AddDate(0, 0, index)
	programs := make([]iptv.Program, 0, len(resp.Data))

	for _, item := range resp.Data {
		start, end, err := parseShandongTimes(baseDate, item.StartTime, item.EndTime)
		if err != nil {
			c.logger.Warn(fmt.Sprintf("时间解析跳过: 节目[%s] 错误:%v", item.ProgName, err))
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

// parseShandongTimes 时间解析（含跨天处理）
func parseShandongTimes(baseDate time.Time, startStr, endStr string) (time.Time, time.Time, error) {
	// 清理时间字符串（处理类似"00:12:00"的情况）
	const timeFormat = "15:04"
	if len(startStr) > 5 { startStr = startStr[:5] }
	if len(endStr) > 5 { endStr = endStr[:5] }

	// 解析时间
	startTime, err := time.ParseInLocation(timeFormat, startStr, baseDate.Location())
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("开始时间解析失败: %w", err)
	}
	endTime, err := time.ParseInLocation(timeFormat, endStr, baseDate.Location())
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("结束时间解析失败: %w", err)
	}

	// 创建日期时间对象
	start := time.Date(
		baseDate.Year(), baseDate.Month(), baseDate.Day(),
		startTime.Hour(), startTime.Minute(), 0, 0, baseDate.Location(),
	)
	end := time.Date(
		baseDate.Year(), baseDate.Month(), baseDate.Day(),
		endTime.Hour(), endTime.Minute(), 0, 0, baseDate.Location(),
	)

	// 处理跨天
	if end.Before(start) {
		end = end.AddDate(0, 0, 1)
	}

	return start, end, nil
}

// setShandongCookies 设置山东特有Cookies
func (c *Client) setShandongCookies(req *http.Request, token *Token, channel *iptv.Channel) {
	cookies := []*http.Cookie{
		{Name: "JSESSIONID", Value: token.JSESSIONID},
		{Name: "STARV_TIMESHFTCID", Value: channel.ChannelID},
		{Name: "STARV_TIMESHFTCNAME", Value: url.QueryEscape(channel.ChannelName)}, // 确保URL编码
		{Name: "maidianFlag", Value: "1"},
		{Name: "navNameFocus", Value: "3"},
		{Name: "channelTip", Value: "1"},
		{Name: "jumpTime", Value: "0"},
		{Name: "lastChanNum", Value: "1"},
	}
	
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
}