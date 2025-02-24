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
)

// 修正JSON结构体定义（匹配实际接口返回字段）
type ShandongChannelProgramListResult struct {
	Result []ShandongChannelProgramList `json:"result"` // 保持与原接口字段一致
}

type ShandongChannelProgramList struct {
	ProgName    string `json:"progName"`
	StartTime   string `json:"startTime"`  // 实际字段名
	EndTime     string `json:"endTime"`    // 实际字段名
	ProgID      string `json:"progId"`
	// 补充其他必要字段...
}

// getShandongChannelProgramList 获取频道节目单
func (c *Client) getShandongChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	dateProgramList := make([]iptv.DateProgram, 0, daysBefore+daysAfter+1)
	now := time.Now().Local()

	// 获取日期范围：7天前 到 1天后
	for offset := -daysBefore; offset <= daysAfter; offset++ {
		targetDate := now.AddDate(0, 0, offset)
		dateStr := targetDate.Format("20060102")

		// 获取指定日期的节目单
		programList, err := c.getShandongChannelDateProgram(ctx, token, channel.ChannelID, dateStr)
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				continue
			}
			c.logger.Warn("获取节目单失败",
				zap.String("channel", channel.ChannelName),
				zap.String("date", dateStr),
				zap.Error(err))
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

// getShandongChannelDateProgram 获取指定日期节目单（修正请求参数）
func (c *Client) getShandongChannelDateProgram(ctx context.Context, token *Token, channelId string, dateStr string) ([]iptv.Program, error) {
	// 构造符合原接口要求的URL
	reqURL := fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgListByIndex.jsp", c.host)
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置原始请求参数
	q := req.URL.Query()
	q.Add("Action", "channelProgramList")
	q.Add("channelId", channelId)
	q.Add("date", dateStr)  // 关键：使用标准日期参数
	req.URL.RawQuery = q.Encode()

	// 设置认证信息
	req.AddCookie(&http.Cookie{Name: "JSESSIONID", Value: token.JSESSIONID})
	req.AddCookie(&http.Cookie{Name: "telecomToken", Value: token.UserToken})
	c.setCommonHeaders(req)

	// 发送请求...(同原代码处理逻辑)

	return c.parseProgramData(respBody, dateStr)
}

// parseProgramData 解析节目数据（优化时间处理）
func (c *Client) parseProgramData(rawData []byte, dateStr string) ([]iptv.Program, error) {
	var resp ShandongChannelProgramListResult
	if err := json.Unmarshal(rawData, &resp); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
	}

	baseDate, err := time.ParseInLocation("20060102", dateStr, time.Local)
	if err != nil {
		return nil, fmt.Errorf("日期解析失败: %w", err)
	}

	programs := make([]iptv.Program, 0, len(resp.Result))
	for _, item := range resp.Result {
		// 解析完整时间戳（假设接口返回格式为 "2006-01-02 15:04:05"）
		startTime, err := time.ParseInLocation("2006-01-02 15:04:05", 
			fmt.Sprintf("%s %s", dateStr[:8], item.StartTime), // 拼接完整日期
			time.Local)
		if err != nil {
			c.logger.Warn("时间解析失败",
				zap.String("startTime", item.StartTime),
				zap.Error(err))
			continue
		}

		// 处理跨天节目
		endTime, err := time.ParseInLocation("2006-01-02 15:04:05", 
			fmt.Sprintf("%s %s", dateStr[:8], item.EndTime), 
			time.Local)
		if err != nil {
			c.logger.Warn("时间解析失败",
				zap.String("endTime", item.EndTime),
				zap.Error(err))
			continue
		}

		// 自动修正跨天节目
		if endTime.Before(startTime) {
			endTime = endTime.AddDate(0, 0, 1)
		}

		programs = append(programs, iptv.Program{
			ProgramName:     item.ProgName,
			BeginTimeFormat: startTime.Format("20060102150405"),
			EndTimeFormat:   endTime.Format("20060102150405"),
			StartTime:       startTime.Format("15:04"),
			EndTime:         endTime.Format("15:04"),
		})
	}

	return programs, nil
}