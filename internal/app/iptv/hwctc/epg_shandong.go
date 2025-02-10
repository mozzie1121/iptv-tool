package hwctc

import (
	//"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iptv/internal/app/iptv"
	"net/http"
	//"strconv"
	"time"
)

type ShandongChannelProgramListResult struct {
	Data  []ShandongChannelProgramList `json:"data"`
	Title []string                `json:"title"`
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

// getShandongChannelProgramList 获取指定频道的节目单列表（hb）
func (c *Client) getShandongChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	// 获取未来一天的日期
	tomorrow := time.Now().AddDate(0, 0, 1)
	tomorrow = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, tomorrow.Location())

	// 根据当前频道的时移范围，预估EPG的查询时间范围（加上未来一天）
	epgBackDay := int(channel.TimeShiftLength.Hours()/24) + 1
	// 限制EPG查询的最大时间范围
	if epgBackDay > maxBackDay {
		epgBackDay = maxBackDay
	}

	// 从未来一天开始往前，倒查多个日期的节目单
	dateProgramList := make([]iptv.DateProgram, 0, epgBackDay+1)
	for i := 0; i <= epgBackDay; i++ {
		// 获取起止时间
		startDate := tomorrow.AddDate(0, 0, -i)
		startTime := startDate.Format("20060102") + " 00:00" // 只获取节目的开始时间
		endTime := startDate.Format("20060102") + " 23:59"   // 结束时间设置为当天23:59

		// 获取指定日期的节目单列表
		programList, err := c.getShandongChannelDateProgram(ctx, token, channel.ChannelID, startTime, endTime, 0) // index starts from 0
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				return nil, err
			}
			c.logger.Sugar().Warnf("Failed to get the program list for channel %s on %s. Error: %v", channel.ChannelName, startDate.Format("20060102"), err)
			continue
		}

		dateProgramList = append(dateProgramList, iptv.DateProgram{
			Date:        startDate,
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

	return parseShandongChannelDateProgram(result)
}

// parseShandongChannelDateProgram 解析频道节目单列表
func parseShandongChannelDateProgram(rawData []byte) ([]iptv.Program, error) {
	// 解析json
	var resp ShandongChannelProgramListResult
	if err := json.Unmarshal(rawData, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, ErrChProgListIsEmpty
	}

	// 遍历单个日期中的节目单
	programList := make([]iptv.Program, 0, len(resp.Data))
	for _, rawProg := range resp.Data {
		// 检查时间字符串是否为空
		if rawProg.StartTime == "" || rawProg.EndTime == "" {
			return nil, errors.New("StartTime or EndTime is empty")
		}

		// 修改时间解析格式为只包含时和分
		startTime, err := time.Parse("15:04", rawProg.StartTime) // 只解析时和分
		if err != nil {
			return nil, fmt.Errorf("error parsing StartTime: %v", err)
		}
		endTime, err := time.Parse("15:04", rawProg.EndTime) // 只解析时和分
		if err != nil {
			return nil, fmt.Errorf("error parsing EndTime: %v", err)
		}

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

