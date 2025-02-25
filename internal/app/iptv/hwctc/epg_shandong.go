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
	playbackDays = 7 // 回看天数（过去7天）
	previewDays  = 5 // 预告天数（未来5天）
	dateFormat   = "20060102"
	timeFormat   = "150405"
)

var (
	ErrEPGApiNotFound  = errors.New("EPG API 404 not found")
	ErrChProgListIsEmpty = errors.New("channel program list is empty")
)

type sdProgramResponse struct {
	Data []struct {
		ProgName    string `json:"progName"`
		StartTime   string `json:"startTime"`
		EndTime     string `json:"endTime"`
		SubProgName string `json:"subProgName"`
		ProgId      string `json:"progId"`
	} `json:"data"`
}

type Client struct {
	httpClient *http.Client
	host       string
	logger     iptv.Logger
}

func NewClient(host string, logger iptv.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		host:       host,
		logger:     logger,
	}
}

// GetChannelProgramList 获取频道节目单（主入口）
func (c *Client) GetChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	datePrograms := make([]iptv.DateProgram, 0, playbackDays+previewDays+1)
	now := time.Now()

	for offset := -playbackDays; offset <= previewDays; offset++ {
		targetDate := now.AddDate(0, 0, offset)
		dateStr := targetDate.Format(dateFormat)

		programs, err := c.getDateProgram(ctx, token, channel, dateStr)
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				continue
			}
			c.logger.Warn(fmt.Sprintf("[%s] 获取节目单失败 日期:%s 错误:%v",
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

// getDateProgram 获取指定日期的节目单
func (c *Client) getDateProgram(ctx context.Context, token *Token, channel *iptv.Channel, dateStr string) ([]iptv.Program, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgList.jsp", c.host), nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	query := req.URL.Query()
	query.Add("CHANNELID", channel.ChannelID)
	query.Add("date", dateStr)
	req.URL.RawQuery = query.Encode()

	c.setRequestHeaders(req)
	c.setAuthCookies(req, token, channel)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrEPGApiNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("非预期状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return c.parseProgramResponse(body, dateStr)
}

// parseProgramResponse 解析节目单响应
func (c *Client) parseProgramResponse(data []byte, dateStr string) ([]iptv.Program, error) {
	var resp sdProgramResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
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
		start, err := parseProgramTime(baseDate, item.StartTime)
		if err != nil {
			c.logger.Warn(fmt.Sprintf("时间解析跳过: %s %s", item.StartTime, err))
			continue
		}

		end, err := parseProgramTime(baseDate, item.EndTime)
		if err != nil {
			c.logger.Warn(fmt.Sprintf("时间解析跳过: %s %s", item.EndTime, err))
			continue
		}

		// 处理跨天节目
		if end.Before(start) {
			end = end.AddDate(0, 0, 1)
		}

		programs = append(programs, iptv.Program{
			ProgramName:     item.ProgName,
			BeginTimeFormat: start.Format(timeFormat),
			EndTimeFormat:   end.Format(timeFormat),
			StartTime:       start.Format("15:04"),
			EndTime:         end.Format("15:04"),
		})
	}

	return programs, nil
}

// parseProgramTime 解析节目时间
func parseProgramTime(base time.Time, t string) (time.Time, error) {
	if len(t) > 5 {
		t = t[:5]
	}
	
	parsed, err := time.ParseInLocation("15:04", t, base.Location())
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

// setRequestHeaders 设置公共请求头
func (c *Client) setRequestHeaders(req *http.Request) {
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

// setAuthCookies 设置认证Cookies
func (c *Client) setAuthCookies(req *http.Request, token *Token, channel *iptv.Channel) {
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