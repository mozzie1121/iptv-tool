package router

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"iptv/internal/app/iptv"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// 导出全局变量（原子指针）
var (
	EpgPtr = &epgPtr // 导出原子指针的地址，外部通过 EpgPtr.Load() 读取
)


// UpdateEPG 更新EPG缓存数据（供外部调用）
func UpdateEPG(ctx context.Context, client iptv.Client) error {
	return updateEPG(ctx, client)
}

// GetXmlEPG 生成 XMLTV 结构（供外部调用）
func GetXmlEPGData(chProgLists []iptv.ChannelProgramList, backDay int) *XmlEPG {
	return getXmlEPG(chProgLists, backDay)
}

const (
	xmltvGenInfoName = "iptv-tool"
	xmltvGenInfoUrl  = "https://github.com/super321/iptv-tool"

	xmltvGzipFilename = "epg.xml.gz"
)

var (
	// 缓存最新的节目单数据
	epgPtr atomic.Pointer[[]iptv.ChannelProgramList]
)

// ChannelDateJsonEPG 频道的JSON格式EPG
type ChannelDateJsonEPG struct {
	ChannelName string    `json:"channel_name"`
	Date        string    `json:"date"`
	EPGData     []JsonEPG `json:"epg_data"`
}

// JsonEPG JSON格式EPG
type JsonEPG struct {
	Title string `json:"title"` // 标题
	Desc  string `json:"desc"`  // 描述
	Start string `json:"start"` // 开始时间
	End   string `json:"end"`   // 结束时间
}

// GetJsonEPG 获取JSON格式的EPG
func GetJsonEPG(c *gin.Context) {
	// 获取频道名称
	chName := c.Query("ch")
	// 获取日期
	dateStr := c.DefaultQuery("date", time.Now().Format("2006-01-02"))

	// 校验频道名称是否为空
	if chName == "" {
		logger.Warn("The name of the channel is null.")
		// 返回响应
		c.Status(http.StatusBadRequest)
		return
	}

	// 解析日期
	date, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		logger.Error("Date format error", zap.Error(err))
		c.Status(http.StatusBadRequest)
		return
	}

	// 空响应
	emptyResp := ChannelDateJsonEPG{
		ChannelName: chName,
		Date:        dateStr,
		EPGData:     []JsonEPG{},
	}

	// 如果缓存的节目单列表为空则直接返回空数据
	chProgLists := *epgPtr.Load()
	if len(chProgLists) == 0 {
		c.PureJSON(http.StatusOK, &emptyResp)
		return
	}

	// 根据频道名称查询到该频道所有日期的节目单列表
	var tagerChProgList *iptv.ChannelProgramList
	for _, chProgList := range chProgLists {
		if chProgList.ChannelName == chName {
			tagerChProgList = &chProgList
			break
		}
	}
	if tagerChProgList == nil || len(tagerChProgList.DateProgramList) == 0 {
		c.PureJSON(http.StatusOK, &emptyResp)
		return
	}

	// 查询该频道指定日期的节目单列表
	dateEPGData := make([]JsonEPG, 0)
	for _, dateProgList := range tagerChProgList.DateProgramList {
		if dateProgList.Date.Equal(date) {
			if len(dateProgList.ProgramList) > 0 {
				for _, program := range dateProgList.ProgramList {
					dateEPGData = append(dateEPGData, JsonEPG{
						Title: program.ProgramName,
						Start: program.StartTime,
						End:   program.EndTime,
					})
				}
			}
			break
		}
	}

	// 返回最终响应
	c.PureJSON(http.StatusOK, &ChannelDateJsonEPG{
		ChannelName: chName,
		Date:        dateStr,
		EPGData:     dateEPGData,
	})
}

// XmlEPG XMLTV格式的EPG
type XmlEPG struct {
	XMLName           xml.Name          `xml:"tv"`
	SourceInfoUrl     string            `xml:"source-info-url,attr,omitempty"`
	SourceInfoName    string            `xml:"source-info-name,attr,omitempty"`
	SourceDataUrl     string            `xml:"source-data-url,attr,omitempty"`
	GeneratorInfoName string            `xml:"generator-info-name,attr,omitempty"`
	GeneratorInfoUrl  string            `xml:"generator-info-url,attr,omitempty"`
	Channels          []XmlEPGChannel   `xml:"channel,omitempty"`
	Programmes        []XmlEPGProgramme `xml:"programme,omitempty"`
}

type XmlEPGChannel struct {
	Id          string         `xml:"id,attr"`
	DisplayName *XmlEPGDisplay `xml:"display-name"`
}

type XmlEPGProgramme struct {
	Start   string         `xml:"start,attr"`
	Stop    string         `xml:"stop,attr"`
	Channel string         `xml:"channel,attr"`
	Title   *XmlEPGDisplay `xml:"title"`
	Desc    *XmlEPGDisplay `xml:"desc,omitempty"`
}

type XmlEPGDisplay struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

// GetXmlEPG 返回XMLTV格式的EPG
func GetXmlEPG(c *gin.Context) {
	var err error

	// 保留过去几天的节目单
	backDay := 0
	backDayStr := c.Query("backDay")
	if backDayStr != "" {
		if backDay, err = strconv.Atoi(backDayStr); err != nil {
			backDay = 0
		}
	}

	// 如果缓存的节目单列表为空则直接返回空数据
	chProgLists := *epgPtr.Load()
	if len(chProgLists) == 0 {
		c.XML(http.StatusOK, &XmlEPG{
			GeneratorInfoName: xmltvGenInfoName,
			GeneratorInfoUrl:  xmltvGenInfoUrl,
		})
		return
	}

	xmlEPG := getXmlEPG(chProgLists, backDay)

	c.XML(http.StatusOK, xmlEPG)
}

func GetXmlEPGWithGzip(c *gin.Context) {
	var err error

	// 保留过去几天的节目单
	backDay := 0
	backDayStr := c.Query("backDay")
	if backDayStr != "" {
		if backDay, err = strconv.Atoi(backDayStr); err != nil {
			backDay = 0
		}
	}

	var xmlEPG *XmlEPG
	// 如果缓存的节目单列表为空则直接返回空数据
	chProgLists := *epgPtr.Load()
	if len(chProgLists) == 0 {
		xmlEPG = &XmlEPG{
			GeneratorInfoName: xmltvGenInfoName,
			GeneratorInfoUrl:  xmltvGenInfoUrl,
		}
	} else {
		xmlEPG = getXmlEPG(chProgLists, backDay)
	}

	// 将结构体数据转换为XML，并进行格式化
	xmlData, err := xml.MarshalIndent(xmlEPG, "", "  ")
	if err != nil {
		logger.Error("Failed to marshal xml.", zap.Error(err))
		c.Status(http.StatusInternalServerError)
		return
	}

	// 设置HTTP头，通知浏览器这是一个二进制流文件
	c.Header("Transfer-Encoding", "gzip")                                                      // 说明文件是gzip压缩格式
	c.Header("Content-Type", "application/octet-stream")                                       // 说明是二进制文件
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", xmltvGzipFilename)) // 指定下载文件名

	// 创建一个gzip压缩的Writer，并将XML数据写入其中
	gzipWriter := gzip.NewWriter(c.Writer)
	defer gzipWriter.Close()

	// 写入xml头
	if _, err = gzipWriter.Write([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")); err != nil {
		logger.Error("Failed to write xml header.", zap.Error(err))
		c.Status(http.StatusInternalServerError)
		return
	}

	// 写入xml内容
	if _, err = gzipWriter.Write(xmlData); err != nil {
		logger.Error("Failed to write xml data.", zap.Error(err))
		c.Status(http.StatusInternalServerError)
		return
	}
}

// getXmlEPG 将频道节目单转为xmltv格式
func getXmlEPG(chProgLists []iptv.ChannelProgramList, backDay int) *XmlEPG {
	backTime := time.Now().AddDate(0, 0, -backDay)
	backTime = time.Date(backTime.Year(), backTime.Month(), backTime.Day(), 0, 0, 0, 0, backTime.Location())

	channels := make([]XmlEPGChannel, 0, len(chProgLists))
	programmes := make([]XmlEPGProgramme, 0)
	for _, chProgList := range chProgLists {
		// 获取频道的相关信息
		channels = append(channels, XmlEPGChannel{
			Id: chProgList.ChannelId,
			DisplayName: &XmlEPGDisplay{
				Lang:  "zh",
				Value: chProgList.ChannelName,
			},
		})

		if len(chProgList.DateProgramList) == 0 {
			continue
		}

		for _, dateProgList := range chProgList.DateProgramList {
			if len(dateProgList.ProgramList) == 0 ||
				(backDay > 0 && !backTime.Before(dateProgList.Date)) {
				continue
			}
			for _, program := range dateProgList.ProgramList {
				// 获取节目的相关信息
				programmes = append(programmes, XmlEPGProgramme{
					Start:   program.BeginTimeFormat + " +0800",
					Stop:    program.EndTimeFormat + " +0800",
					Channel: chProgList.ChannelId,
					Title: &XmlEPGDisplay{
						Lang:  "zh",
						Value: program.ProgramName,
					},
				})
			}
		}
	}

	return &XmlEPG{
		GeneratorInfoName: xmltvGenInfoName,
		GeneratorInfoUrl:  xmltvGenInfoUrl,
		Channels:          channels,
		Programmes:        programmes,
	}
}

// updateEPG 更新缓存的节目单数据
func updateEPG(ctx context.Context, iptvClient iptv.Client) error {
	// 获取缓存的所有频道列表
	channels := *channelsPtr.Load()
	if len(channels) == 0 {
		return errors.New("no channels")
	}

	// 获取所有频道的节目单列表
	allChProgramList, err := iptvClient.GetAllChannelProgramList(ctx, channels)
	if err != nil {
		return err
	}

	logger.Sugar().Infof("EPG data updated, rows: %d.", len(allChProgramList))
	// 更新缓存的频道列表
	epgPtr.Store(&allChProgramList)

	return nil
}
