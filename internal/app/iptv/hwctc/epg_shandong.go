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

type sdIptvProgramListResult struct {
	Data  []sdIptvProgram `json:"data"`
	Title []string        `json:"title"`
}

type sdIptvProgram struct {
	ProgName    string `json:"progName"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
	SubProgName string `json:"subProgName"`
	State       string `json:"state"`
	ProgId      string `json:"progId"`
}

// getSdIptvChannelProgramList 获取山东IPTV指定频道的节目单列表
func (c *Client) getSdIptvChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	epgBackDay := int(channel.TimeShiftLength.Hours()/24) + 1
	if epgBackDay > maxBackDay {
		epgBackDay = maxBackDay
	}

	dateProgramList := make([]iptv.DateProgram, 0, epgBackDay+1)

	for i := 0; i <= epgBackDay; i++ {
		indexStr := strconv.Itoa(i)
		programs, err := c.getSdIptvChannelIndexProgram(ctx, token, channel, indexStr)
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				return nil, err
			}
			c.logger.Sugar().Warnf("获取频道 %s 索引 %s 节目单失败: %v", channel.ChannelName, indexStr, err)
			continue
		}

		date := time.Now().AddDate(0, 0, i)
		date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())

		dateProgramList = append(dateProgramList, iptv.DateProgram{
			Date:        date,
			ProgramList: programs,
		})
	}

	return &iptv.ChannelProgramList{
		ChannelId:       channel.ChannelID,
		ChannelName:     channel.ChannelName,
		DateProgramList: dateProgramList,
	}, nil
}

// getSdIptvChannelIndexProgram 获取指定频道和索引的节目单
func (c *Client) getSdIptvChannelIndexProgram(ctx context.Context, token *Token, channel *iptv.Channel, indexStr string) ([]iptv.Program, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgListByIndex.jsp", c.host), nil)
	if err != nil {
		return nil, err
	}

	params := req.URL.Query()
	params.Add("CHANNELID", channel.ChannelID)
	params.Add("index", indexStr)
	req.URL.RawQuery = params.Encode()

	c.setSdCommonHeaders(req)
	c.setSdCookies(req, token, channel)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrEPGApiNotFound
	} else if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP状态码: %d", resp.StatusCode)
	}

	rawData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseSdIptvProgramList(rawData, indexStr)
}

// setSdCommonHeaders 设置山东IPTV专用请求头
func (c *Client) setSdCommonHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "webkit;Resolution(PAL,720P,1080P)")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/chanMiniList.html", c.host))
	req.Header.Set("X-Requested-With", "com.hisense.iptv")
}

// setSdCookies 设置山东IPTV所需的Cookie
func (c *Client) setSdCookies(req *http.Request, token *Token, channel *iptv.Channel) {
	req.AddCookie(&http.Cookie{Name: "JSESSIONID", Value: token.JSESSIONID})
	req.AddCookie(&http.Cookie{Name: "STARV_TIMESHFTCID", Value: channel.ChannelID})
	req.AddCookie(&http.Cookie{Name: "STARV_TIMESHFTCNAME", Value: url.QueryEscape(channel.ChannelName)})
	req.AddCookie(&http.Cookie{Name: "maidianFlag", Value: "1"})
	req.AddCookie(&http.Cookie{Name: "navNameFocus", Value: "3"})
	req.AddCookie(&http.Cookie{Name: "jumpTime", Value: "0"})
	req.AddCookie(&http.Cookie{Name: "channelTip", Value: "1"})
	req.AddCookie(&http.Cookie{Name: "lastChanNum", Value: "1"})
}

// parseSdIptvProgramList 解析节目单响应
func parseSdIptvProgramList(rawData []byte, indexStr string) ([]iptv.Program, error) {
	var resp sdIptvProgramListResult
	if err := json.Unmarshal(rawData, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, ErrChProgListIsEmpty
	}

	index, _ := strconv.Atoi(indexStr)
	baseDate := time.Now().AddDate(0, 0, index)
	baseDate = time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), 0, 0, 0, 0, baseDate.Location())

	programs := make([]iptv.Program, 0, len(resp.Data))
	for _, p := range resp.Data {
		startTime, err := time.ParseInLocation("15:04", p.StartTime, time.Local)
		if err != nil {
			continue
		}

		endTimeStr := p.EndTime
		if len(endTimeStr) > 5 {
			endTimeStr = endTimeStr[:5]
		}
		endTime, err := time.ParseInLocation("15:04", endTimeStr, time.Local)
		if err != nil {
			continue
		}

		startDateTime := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), startTime.Hour(), startTime.Minute(), 0, 0, time.Local)
		endDateTime := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), endTime.Hour(), endTime.Minute(), 0, 0, time.Local)

		if endDateTime.Before(startDateTime) {
			endDateTime = endDateTime.AddDate(0, 0, 1)
		}

		programs = append(programs, iptv.Program{
			ProgramName:     p.ProgName,
			BeginTimeFormat: startDateTime.Format("20060102150405"),
			EndTimeFormat:   endDateTime.Format("20060102150405"),
			StartTime:       startDateTime.Format("15:04"),
			EndTime:         endDateTime.Format("15:04"),
		})
	}

	return programs, nil
}