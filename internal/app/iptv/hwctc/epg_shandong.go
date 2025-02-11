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

type ShandongChannelProgramListResult struct {
	Data  []ShandongChannelProgramList `json:"data"`
	Title []string                     `json:"title"`
}

type ShandongChannelProgramList struct {
	ProgName     string `json:"progName"`
	ScrollFlag   int    `json:"scrollFlag"`
	StartTime    string `json:"startTime"`
	EndTime      string `json:"endTime"`
	SubProgName  string `json:"subProgName"`
	State        string `json:"state"`
	ProgID       string `json:"progId"`
}

// getShandongChannelProgramList 获取指定频道的节目单列表
func (c *Client) getShandongChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	// 获取今天的日期
	today := time.Now().Truncate(24 * time.Hour)

	// 根据当前频道的时移范围，预估 EPG 的查询时间范围
	epgBackDay := int(channel.TimeShiftLength.Hours() / 24)
	// 限制 EPG 查询的最大时间范围
	if epgBackDay > maxBackDay {
		epgBackDay = maxBackDay
	}

	// 从今天开始往前，倒查多个日期的节目单
	dateProgramList := make([]iptv.DateProgram, 0, epgBackDay)
	for i := 0; i < epgBackDay; i++ {
		// 获取过去日期
		pastDate := today.AddDate(0, 0, -i) // 获取今天及之前的日期
		// 计算开始与结束时间
		startTime := pastDate.Format("20060102") + " 00:00"
		endTime := pastDate.Format("20060102") + " 23:59"

		// 计算 index
		index := -i
		if index < -6 {
			index = -6
		}

		// 获取指定日期的节目单列表
		programList, err := c.getShandongChannelDateProgram(ctx, token, channel.ChannelID, startTime, endTime, index) // index starts from 0
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				return nil, err
			}
			c.logger.Sugar().Warnf("Failed to get the program list for channel %s on %s. Error: %v", channel.ChannelName, pastDate.Format("20060102"), err)
			continue
		}

		// 添加日期与对应的节目单
		dateProgramList = append(dateProgramList, iptv.DateProgram{
			Date:        pastDate,
			ProgramList: programList,
		})
	}

	return &iptv.ChannelProgramList{
		ChannelId:       channel.ChannelID,
		ChannelName:     channel.ChannelName,
		DateProgramList: dateProgramList,
	}, nil
}

// getShandongChannelDateProgram 获取指定频道的某日期的节目单列表
func (c *Client) getShandongChannelDateProgram(ctx context.Context, token *Token, channelId string, startTime string, endTime string, index int) ([]iptv.Program, error) {
	// 创建请求
	url := fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgListByIndex.jsp?CHANNELID=%s&index=%d", c.host, channelId, index)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	// 设置请求头
	c.setCommonHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "com.hisense.iptv")

	// 设置 Cookie
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

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrEPGApiNotFound
	} else if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status code: %d", resp.StatusCode)
	}

	// 解析响应内容
	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 在这里调用解析函数，不再需要传递日期，现在可以通过 index 知道所需日期
	return parseShandongChannelDateProgram(result, index)
}

// parseShandongChannelDateProgram 解析频道节目单列表
func parseShandongChannelDateProgram(rawData []byte, index int) ([]iptv.Program, error) {
	// 解析 json
	var resp ShandongChannelProgramListResult
	if err := json.Unmarshal(rawData, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, ErrChProgListIsEmpty
	}

	// 计算日期
	date := time.Now().AddDate(0, 0, index) // 根据 index 计算日期

	// 遍历单个日期中的节目单
	programList := make([]iptv.Program, 0, len(resp.Data))
	for _, rawProg := range resp.Data {
		// 检查时间字符串是否为空
		if rawProg.StartTime == "" || rawProg.EndTime == "" {
			return nil, errors.New("StartTime or EndTime is empty")
		}

		// 解析起始时间（只需要小时和分钟）
		startTime, err := time.Parse("15:04", rawProg.StartTime)
		if err != nil {
			return nil, fmt.Errorf("error parsing StartTime: %v", err)
		}

		// 解析结束时间（包含秒）
		var endTime time.Time
		if strings.Contains(rawProg.EndTime, ":") {
			endTime, err = time.Parse("15:04:05", rawProg.EndTime) // 解析带秒的结束时间
			if err != nil {
				return nil, fmt.Errorf("error parsing EndTime: %v", err)
			}
		} else {
			return nil, errors.New("EndTime format is invalid, it should contain seconds")
		}

		// 将具体日期和时间合并
		startTime = time.Date(date.Year(), date.Month(), date.Day(), startTime.Hour(), startTime.Minute(), 0, 0, startTime.Location())
		endTime = time.Date(date.Year(), date.Month(), date.Day(), endTime.Hour(), endTime.Minute(), endTime.Second(), 0, endTime.Location())

		programList = append(programList, iptv.Program{
			ProgramName:     rawProg.ProgName,
			BeginTimeFormat: startTime.Format("20060102150405"),
			EndTimeFormat:   endTime.Format("20060102150405"),
			StartTime:       startTime.Format("15:04"),
			EndTime:         endTime.Format("15:04"),
		})
	}

	return programList, nil
}
