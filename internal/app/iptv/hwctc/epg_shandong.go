package hwctc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iptv/internal/app/iptv"
	"net/http"
	"time"
	"strings"
)

// 新增常量定义
const (
	daysBefore = 7 // 需要获取的历史天数
	daysAfter  = 1 // 需要获取的未来天数
)

// parseTime 通用时间解析函数（兼容HH:mm和HH:mm:ss）
func parseTime(tStr string) (time.Time, error) {
	formats := []string{"15:04:05", "15:04"}
	for _, format := range formats {
		t, err := time.Parse(format, tStr)
		if err == nil {
			// 如果秒数未定义，补充为00
			if t.Second() == 0 && strings.Count(tStr, ":") == 1 {
				return t, nil
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间字符串: %s", tStr)
}

// getShandongChannelProgramList 获取频道节目单（-7天到+1天）
func (c *Client) getShandongChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	dateProgramList := make([]iptv.DateProgram, 0, daysBefore+daysAfter+1)
	now := time.Now()

	// 获取从-7天到+1天的节目单
	for offset := -daysBefore; offset <= daysAfter; offset++ {
		// 计算目标日期
		targetDate := now.AddDate(0, 0, offset)
		dateStr := targetDate.Format("2006-01-02")

		// 构造时间范围（全天）
		startTime := targetDate.Format("20060102") + " 00:00"
		endTime := targetDate.Format("20060102") + " 23:59"

		// 获取当日节目单（index=offset）
		programList, err := c.getShandongChannelDateProgram(ctx, token, channel.ChannelID, startTime, endTime, offset)
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				c.logger.Sugar().Debugf("频道 %s 在 %s 无节目单", channel.ChannelName, dateStr)
				continue
			}
			c.logger.Sugar().Warnf("获取 %s 节目单失败: %v", dateStr, err)
			continue
		}

		dateProgramList = append(dateProgramList, iptv.DateProgram{
			Date:        targetDate.Truncate(24 * time.Hour),
			ProgramList: programList,
		})
	}

	return &iptv.ChannelProgramList{
		ChannelId:       channel.ChannelID,
		ChannelName:     channel.ChannelName,
		DateProgramList: dateProgramList,
	}, nil
}

// getShandongChannelDateProgram 获取指定日期节目单（带改进的时间处理）
func (c *Client) getShandongChannelDateProgram(ctx context.Context, token *Token, channelId string, startTime string, endTime string, index int) ([]iptv.Program, error) {
	url := fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgListByIndex.jsp?CHANNELID=%s&index=%d", 
		c.host, channelId, index)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置公共请求头
	c.setCommonHeaders(req)
	req.Header.Set("X-Requested-With", "com.hisense.iptv")
	req.AddCookie(&http.Cookie{Name: "JSESSIONID", Value: token.JSESSIONID})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrEPGApiNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("非200状态码: %d", resp.StatusCode)
	}

	rawData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return c.parseProgramDataWithCrossDay(rawData, index)
}

// parseProgramDataWithCrossDay 改进的解析函数（处理跨天节目）
func (c *Client) parseProgramDataWithCrossDay(rawData []byte, index int) ([]iptv.Program, error) {
	var resp ShandongChannelProgramListResult
	if err := json.Unmarshal(rawData, &resp); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, ErrChProgListIsEmpty
	}

	// 根据index计算基准日期
	baseDate := time.Now().AddDate(0, 0, index)
	programs := make([]iptv.Program, 0, len(resp.Data))

	for _, item := range resp.Data {
		// 解析时间（兼容不同格式）
		startTime, err := parseTime(item.StartTime)
		if err != nil {
			c.logger.Sugar().Warnf("跳过无效开始时间: %s", item.StartTime)
			continue
		}

		endTime, err := parseTime(item.EndTime)
		if err != nil {
			c.logger.Sugar().Warnf("跳过无效结束时间: %s", item.EndTime)
			continue
		}

		// 构建完整时间（考虑跨天情况）
		startFull := time.Date(
			baseDate.Year(), baseDate.Month(), baseDate.Day(),
			startTime.Hour(), startTime.Minute(), startTime.Second(),
			0, baseDate.Location(),
		)

		endFull := time.Date(
			baseDate.Year(), baseDate.Month(), baseDate.Day(),
			endTime.Hour(), endTime.Minute(), endTime.Second(),
			0, baseDate.Location(),
		)

		// 处理跨天节目（结束时间小于开始时间时加1天）
		if endFull.Before(startFull) {
			endFull = endFull.AddDate(0, 0, 1)
		}

		programs = append(programs, iptv.Program{
			ProgramName:     item.ProgName,
			BeginTimeFormat: startFull.Format("20060102150405"),
			EndTimeFormat:   endFull.Format("20060102150405"),
			StartTime:       startFull.Format("15:04"),
			EndTime:         endFull.Format("15:04"),
			Duration:       int(endFull.Sub(startFull).Seconds()),
		})
	}

	return programs, nil
}