package cmds

import (
	"errors"
	"iptv/internal/app/iptv"
	"iptv/internal/app/iptv/hwctc"
	"iptv/internal/pkg/util"
	"net/http"
	"os"
	"path"
	"slices"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

const (
	fileName = "iptv"
)

var (
	supportFileFormat = []string{"txt", "m3u"}
	udpxyURL          string
	format            string
	catchupSource     string
	catchUpMode       string // 新增参数
	multicastFirst    bool
)

func NewChannelCLI() *cobra.Command {
	channelCmd := &cobra.Command{
		Use:   "channel",
		Short: "获取频道列表，并按指定格式生成直播源文件（支持多种回看模式）",
		RunE: func(cmd *cobra.Command, args []string) error {
			// L()：获取全局logger
			logger := zap.L()

			// 校验配置文件
			if err := conf.Validate(); err != nil {
				return err
			}

			// 创建IPTV客户端
			i, err := hwctc.NewClient(&http.Client{
				Timeout: 10 * time.Second,
			}, conf.HWCTC, conf.Key, conf.ServerHost, conf.Headers,
				conf.ChExcludeRule, conf.ChGroupRulesList, conf.ChLogoRuleList)
			if err != nil {
				return err
			}

			// 获取频道列表
			channels, err := i.GetAllChannelList(cmd.Context())
			if err != nil {
				return err
			}

			if len(channels) == 0 {
				return errors.New("no channels found")
			}

			if !slices.Contains(supportFileFormat, format) {
				return errors.New("file format not support")
			}

			// 在当前目录中创建频道文件
			outFileName := fileName + "." + format
			currDir, err := util.GetCurrentAbPathByExecutable()
			if err != nil {
				return err
			}
			filePath := path.Join(currDir, outFileName)
			file, err := os.Create(filePath)
			if err != nil {
				logger.Error("Failed to create a file.", zap.Error(err))
				return err
			}
			defer file.Close()

			var content string
			switch format {
			case "txt":
				// 将获取到的频道列表转换为TXT格式
				content, err = iptv.ToTxtFormat(channels, udpxyURL, multicastFirst)
				if err != nil {
					return err
				}
			case "m3u":
				// 修复点：添加缺失的 catchUpMode 参数
				content, err = iptv.ToM3UFormat(
					channels,
					udpxyURL,
					catchupSource,
					catchUpMode,  // 新增参数
					multicastFirst,
					"", // logoBaseUrl 留空
				)
				if err != nil {
					return err
				}
			}

			// 将结果写入文件
			if _, err = file.WriteString(content); err != nil {
				logger.Error("Failed to write to file.", zap.Error(err))
				return err
			}

			logger.Sugar().Infof("A total of %d channels have been found, all of which have been written to the file %s.", len(channels), outFileName)

			return nil
		},
	}

	// 命令行参数配置
	channelCmd.Flags().StringVarP(&udpxyURL, "udpxy", "u", "", "如果有安装udpxy进行组播转单播，请配置HTTP地址，e.g `http://192.168.1.1:4022`。")
	channelCmd.Flags().StringVarP(&format, "format", "f", "m3u", "生成的直播源文件格式，e.g `m3u或txt`。")
	channelCmd.Flags().StringVarP(&catchupSource, "catchup-source", "s", "?playseek=${(b)yyyyMMddHHmmss}-${(e)yyyyMMddHHmmss}", "回看的请求格式字符串（模式0/1/4时生效）")
	channelCmd.Flags().StringVarP(&catchUpMode, "catch-up-mode", "c", "0",
		`回看模式参数：
0 - 默认模式（使用 catchup-source 参数）
1 - 追加模式
2 - Flussonic 专用格式
3 - Xtream-Codes 兼容格式
4 - 自定义参数模式`)
	channelCmd.Flags().BoolVarP(&multicastFirst, "multicast-first", "m", false, "当频道存在多个URL地址时，是否优先使用组播地址。")

	return channelCmd
}