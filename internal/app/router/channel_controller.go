package router

import (
	"context"
	"errors"
	"fmt"
	"iptv/internal/app/iptv"
	"iptv/internal/pkg/util"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	diypCatchupSource  = "?playseek=${(b)yyyyMMddHHmmss}-${(e)yyyyMMddHHmmss}"
	kodiCatchupSource  = "?playseek={utc:YmdHMS}-{utcend:YmdHMS}"
	flussonicSourceFmt = "?start=${start}&end=${end}&dvr=${duration}"
	xdomoSourceFmt      = "?timeshift=${start}-${end}"
)

var (
	channelsPtr atomic.Pointer[[]iptv.Channel]
)

// GetM3UData 查询直播源m3u
func GetM3UData(c *gin.Context) {

	// 1. 处理 CatchUp 参数
	catchUpMode := c.DefaultQuery("CatchUp", "0")
	if catchUpMode < "0" || catchUpMode > "4" {
		logger.Warn("非法回看模式参数，使用默认值",
			zap.String("input", catchUpMode),
			zap.String("resetTo", "0"))
		catchUpMode = "0"
	}

	// 2. 动态生成 catchupSource（模式优先级高于时间格式）
	var catchupSource string
	switch catchUpMode {
	case "1": // 新增：append 模式（直接使用 csFormat 参数）
        csFormat := c.DefaultQuery("csFormat", "0")
        switch csFormat {
        case "1":
            catchupSource = kodiCatchupSource
        default:
            catchupSource = diypCatchupSource
        }
        logger.Debug("启用追加模式", zap.String("source", catchupSource))
	case "2": // Flussonic 专用格式
		catchupSource = flussonicSourceFmt
		logger.Debug("启用 Flussonic 回看模式")
	case "3": // Xtream-Codes 兼容格式
		catchupSource = xdomoSourceFmt
		logger.Debug("启用 Xdomo 回看模式")
	case "4": // 完全自定义模式
		if custom := c.Query("catchupSource"); custom != "" {
			catchupSource = custom
			logger.Debug("使用自定义回看参数", zap.String("source", custom))
		} else {
			catchupSource = diypCatchupSource
			logger.Warn("自定义模式未提供参数，回退DIYP格式")
		}
	default: // 0/1 使用 csFormat 时间格式
		csFormat := c.DefaultQuery("csFormat", "0")
		switch csFormat {
		case "1":
			catchupSource = kodiCatchupSource
		default:
			catchupSource = diypCatchupSource
		}
		logger.Debug("常规模式选择时间格式",
			zap.String("mode", catchUpMode),
			zap.String("format", csFormat))
	}

	multiFirstStr := c.DefaultQuery("multiFirst", "true")
	multicastFirst, err := strconv.ParseBool(multiFirstStr)
	if err != nil {
		multicastFirst = true
	}

	udpxyName := c.Query("udpxy")
	udpxyURL := getUdpxyURL(udpxyName)

	channels := *channelsPtr.Load()
	if len(channels) == 0 {
		c.Status(http.StatusNotFound)
		return
	}

	logoBaseUrl := fmt.Sprintf("http://%s/logo", c.Request.Host)

	m3uContent, err := iptv.ToM3UFormat(
		channels,
		udpxyURL,
		catchupSource,
		catchUpMode,
		multicastFirst,
		logoBaseUrl,
	)
	if err != nil {
		logger.Error("生成M3U失败",
			zap.Error(err),
			zap.String("mode", catchUpMode))
		c.Status(http.StatusInternalServerError)
		return
	}

	c.String(http.StatusOK, m3uContent)
}

// GetTXTData 查询直播源txt
func GetTXTData(c *gin.Context) {
	multiFirstStr := c.DefaultQuery("multiFirst", "true")
	multicastFirst, err := strconv.ParseBool(multiFirstStr)
	if err != nil {
		multicastFirst = true
	}

	udpxyName := c.Query("udpxy")
	udpxyURL := getUdpxyURL(udpxyName)

	channels := *channelsPtr.Load()
	if len(channels) == 0 {
		c.Status(http.StatusNotFound)
		return
	}

	txtContent, err := iptv.ToTxtFormat(channels, udpxyURL, multicastFirst)
	if err != nil {
		logger.Error("转换频道列表到TXT格式失败", zap.Error(err))
		c.Status(http.StatusOK)
		return
	}

	c.String(http.StatusOK, txtContent)
}

// getUdpxyURL 通过udpxy的名称来获取指定的URL地址
func getUdpxyURL(udpxyName string) string {
	var udpxyURL string
	if udpxyName != "" {
		udpxyURL = udpxyURLs[udpxyName]
	} else {
		for _, k := range util.SortedMapKeys(udpxyURLs) {
			udpxyURL = udpxyURLs[k]
			break
		}
	}
	return udpxyURL
}

// updateChannelsWithRetry 更新缓存的频道数据
func updateChannelsWithRetry(ctx context.Context, iptvClient iptv.Client, maxRetries int) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		if err = updateChannels(ctx, iptvClient); err != nil {
			logger.Sugar().Errorf("更新频道列表失败，%d秒后重试。错误：%v，重试次数：%d", 
				waitSeconds, err, i)
			time.Sleep(waitSeconds * time.Second)
		} else {
			break
		}
	}
	return err
}

// updateChannels 更新缓存的频道数据
func updateChannels(ctx context.Context, iptvClient iptv.Client) error {
	channels, err := iptvClient.GetAllChannelList(ctx)
	if err != nil {
		return err
	}

	if len(channels) == 0 {
		return errors.New("未找到有效频道")
	}

	logger.Sugar().Infof("频道列表已更新，总数：%d", len(channels))
	channelsPtr.Store(&channels)
	return nil
}