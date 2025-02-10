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

type gdhdpublicChannelProgramListResult struct {
	Result []gdhdpublicChannelProgramList `json:"result"`
}

type gdhdpublicChannelProgramList struct {
	Code    string `json:"code"`
	ProID   string `json:"proID"`
	ProFlag string `json:"proflag"`
	Name    string `json:"name"`
	Time    string `json:"time"`
	Endtime string `json:"endtime"`
	Day     string `json:"day"`
}

// getGdhdpublicChannelProgramList 获取指定频道的节目单列表（zj）
func (c *Client) getGdhdpublicChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
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
		date := tomorrow.AddDate(0, 0, -i)
		dateStr := date.Format("20060102")

		// 获取指定日期的节目单列表
		dateProgram, err := c.getGdhdpublicChannelDateProgram(ctx, token, channel.ChannelID, dateStr)
		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				return nil, err
			}
			c.logger.Sugar().Warnf("Failed to get the program list for channel %s on %s. Error: %v", channel.ChannelName, dateStr, err)
			continue
		}

		dateProgramList = append(dateProgramList, *dateProgram)
	}

	return &iptv.ChannelProgramList{
		ChannelId:       channel.ChannelID,
		ChannelName:     channel.ChannelName,
		DateProgramList: dateProgramList,
	}, nil
}

// getGdhdpublicChannelDateProgram 获取指定频道的某日期的节目单列表
func (c *Client) getGdhdpublicChannelDateProgram(ctx context.Context, token *Token, channelId string, dateStr string) (*iptv.DateProgram, error) {
	// 创建请求
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://%s/EPG/jsp/gdhdpublic/Ver.3/common/data.jsp", c.host), nil)
		//fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgListByIndex.jsp", c.host), nil)
	if err != nil {
		return nil, err
	}

	// 增加请求参数
	params := req.URL.Query()
	params.Add("Action", "channelProgramList")
	params.Add("channelId", channelId)
	params.Add("date", dateStr)
	req.URL.RawQuery = params.Encode()

	// 设置请求头
	c.setCommonHeaders(req)

	// 设置Cookie
	req.AddCookie(&http.Cookie{
		Name:  "JSESSIONID",
		Value: token.JSESSIONID,
	})
	req.AddCookie(&http.Cookie{
		Name:  "telecomToken",
		Value: token.UserToken,
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

	return parseGdhdpublicChannelDateProgram(result)
}

// parseGdhdpublicChannelDateProgram 解析频道节目单列表
func parseGdhdpublicChannelDateProgram(rawData []byte) (*iptv.DateProgram, error) {
	// 解析json
	var resp gdhdpublicChannelProgramListResult
	if err := json.Unmarshal(rawData, &resp); err != nil {
		return nil, err
	}

	if len(resp.Result) == 0 {
		return nil, ErrChProgListIsEmpty
	}

	// 遍历单个日期中的节目单
	programList := make([]iptv.Program, 0, len(resp.Result))
	for _, rawProg := range resp.Result {
		bTime, err := time.Parse(time.DateTime, rawProg.Day+" "+rawProg.Time)
		if err != nil {
			return nil, err
		}
		eTime, err := time.Parse(time.DateTime, rawProg.Day+" "+rawProg.Endtime)
		if err != nil {
			return nil, err
		}

		programList = append(programList, iptv.Program{
			ProgramName:     rawProg.Name,
			BeginTimeFormat: bTime.Format("20060102150405"),
			EndTimeFormat:   eTime.Format("20060102150405"),
			StartTime:       bTime.Format("15:04"),
			EndTime:         eTime.Format("15:04"),
		})
	}

	beginTime, err := time.Parse("20060102150405", programList[0].BeginTimeFormat)
	if err != nil {
		return nil, err
	}
	// 时间取整到天
	date := time.Date(beginTime.Year(), beginTime.Month(), beginTime.Day(), 0, 0, 0, 0, beginTime.Location())
	return &iptv.DateProgram{
		Date:        date,
		ProgramList: programList,
	}, nil
}
