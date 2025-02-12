package hwctc

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "iptv/internal/app/iptv"
    "net/http"
    "strings"
    "time"
)

// 常量定义
const (
    daysBefore = 7 // 获取的历史天数
    daysAfter  = 1 // 获取的未来天数
)

// ShandongChannelProgramListResult 代表API返回的节目单结果
type ShandongChannelProgramListResult struct {
    Data  []ShandongChannelProgramList `json:"data"`
    Title []string                     `json:"title"`
}

// ShandongChannelProgramList 代表单个节目信息
type ShandongChannelProgramList struct {
    ProgName     string `json:"progName"`
    ScrollFlag   int    `json:"scrollFlag"`
    StartTime    string `json:"startTime"`
    EndTime      string `json:"endTime"`
    SubProgName  string `json:"subProgName"`
    State        string `json:"state"`
    ProgID       string `json:"progId"`
}

// 通用时间解析函数，兼容HH:mm和HH:mm:ss格式
func parseTime(tStr string) (time.Time, error) {
    formats := []string{"15:04:05", "15:04"}
    for _, format := range formats {
        t, err := time.Parse(format, tStr)
        if err == nil {
            return t, nil
        }
    }
    return time.Time{}, fmt.Errorf("无法解析时间字符串: %s", tStr)
}

// getShandongChannelProgramList 获取频道的节目单列表
func (c *Client) getShandongChannelProgramList(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
    // 定义日期范围，从daysBefore天前到daysAfter天后
    dateProgramList := make([]iptv.DateProgram, 0, daysBefore+daysAfter+1)
    now := time.Now()

    // 定义当前上下文日志记录器
    logger := c.logger.Sugar().With("module", "hwctc", "method", "getShandongChannelProgramList")

    // 遍历从过去7天到未来1天的每一天
    for offset := -daysBefore; offset <= daysAfter; offset++ {
        // 计算目标日期
        targetDate := now.AddDate(0, 0, offset)
        dateStr := targetDate.Format("2006-01-02")

        // 定义开始和结束时间
        startTime := targetDate.Format("20060102") + " 00:00"
        endTime := targetDate.Format("20060102") + " 23:59"

        // 调用获取当日节目单的方法
        programList, err := c.getShandongChannelDateProgram(ctx, token, channel.ChannelID, startTime, endTime, offset)
        if err != nil {
            if errors.Is(err, ErrEPGApiNotFound) {
                logger.Debugf("频道 %s 在 %s 没有找到节目单", channel.ChannelName, dateStr)
                continue
            }
            logger.Warnf("获取 %s 节目单失败: %v", dateStr, err)
            continue
        }

        // 将当日节目单添加到列表中
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

// getShandongChannelDateProgram 获取指定频道某日期的节目单
func (c *Client) getShandongChannelDateProgram(ctx context.Context, token *Token, channelId string, startTime string, endTime string, index int) ([]iptv.Program, error) {
    // 构造API请求URL
    url := fmt.Sprintf("http://%s/EPG/jsp/defaulttrans2/en/datajsp/getTvodProgListByIndex.jsp?CHANNELID=%s&index=%d",
        c.host, channelId, index)

    // 创建HTTP请求
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return nil, fmt.Errorf("创建请求失败: %w", err)
    }

    // 设置请求头
    c.setCommonHeaders(req)
    req.Header.Set("X-Requested-With", "com.hisense.iptv")
    req.AddCookie(&http.Cookie{
        Name:  "JSESSIONID",
        Value: token.JSESSIONID,
    })

    // 执行请求并处理响应
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("请求失败: %w", err)
    }
    defer resp.Body.Close()

    // 检查HTTP状态码
    if resp.StatusCode == http.StatusNotFound {
        return nil, ErrEPGApiNotFound
    }
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("HTTP 请求失败,状态码: %d", resp.StatusCode)
    }

    // 读取响应正文
    rawData, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("读取响应失败: %w", err)
    }

    // 解析响应数据
    return c.parseProgramDataWithCrossDay(rawData, index)
}

// parseProgramDataWithCrossDay 解析包含跨天节目的数据
func (c *Client) parseProgramDataWithCrossDay(rawData []byte, index int) ([]iptv.Program, error) {
    var resp ShandongChannelProgramListResult
    if err := json.Unmarshal(rawData, &resp); err != nil {
        return nil, fmt.Errorf("JSON 解析失败: %w", err)
    }

    if len(resp.Data) == 0 {
        return nil, ErrChProgListIsEmpty
    }

    // 计算基准日期
    baseDate := time.Now().AddDate(0, 0, index)

    // 定义用于存储解析后的节目单
    programs := make([]iptv.Program, 0, len(resp.Data))

    for _, item := range resp.Data {
        // 解析开始时间
        startTime, err := parseTime(item.StartTime)
        if err != nil {
            c.logger.Sugar().Warnf("跳过无效开始时间: %s", item.StartTime)
            continue
        }

        // 解析结束时间
        endTime, err := parseTime(item.EndTime)
        if err != nil {
            c.logger.Sugar().Warnf("跳过无效结束时间: %s", item.EndTime)
            continue
        }

        // 构建完整的开始和结束时间
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

        // 处理跨天情况：如果结束时间早于开始时间,则结束时间加1天
        if endFull.Before(startFull) {
            endFull = endFull.AddDate(0, 0, 1)
        }

        // 计算节目时长(秒)
        duration := int(endFull.Sub(startFull).Seconds())

        programs = append(programs, iptv.Program{
            ProgramName:     item.ProgName,
            BeginTimeFormat: startFull.Format("20060102150405"),
            EndTimeFormat:   endFull.Format("20060102150405"),
            StartTime:       startFull.Format("15:04"),
            EndTime:         endFull.Format("15:04"),
            Duration:       duration,
        })
    }

    return programs, nil
}

// 定义错误类型（在程序其他地方定义）
var (
    ErrEPGApiNotFound    = errors.New("节目单 API 未找到")
    ErrChProgListIsEmpty = errors.New("节目单数据为空")
)
