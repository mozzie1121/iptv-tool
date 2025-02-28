package cmds

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"iptv/internal/app/iptv/hwctc"
	"iptv/internal/app/router"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func NewEPGCLI() *cobra.Command {
	var (
		outputFile string
		backDays   int
		useGzip    bool
	)

	epgCmd := &cobra.Command{
		Use:   "epg",
		Short: "导出XMLTV格式的节目单（支持Gzip压缩）",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zap.L()

			// 初始化客户端（复用全局配置 conf）
			client, err := hwctc.NewClient(
				&http.Client{Timeout: 10 * time.Second},
				conf.HWCTC, conf.Key, conf.ServerHost, conf.Headers,
				conf.ChExcludeRule, conf.ChGroupRulesList, conf.ChLogoRuleList,
			)
			if err != nil {
				return fmt.Errorf("客户端初始化失败: %w", err)
			}

			// 更新EPG数据（复用 router 包逻辑）
			if err := router.UpdateEPG(cmd.Context(), client); err != nil {
				return fmt.Errorf("更新EPG数据失败: %w", err)
			}

			// 获取缓存数据
			chProgLists := *router.EpgPtr.Load()
			if len(chProgLists) == 0 {
				return errors.New("无可用节目单数据")
			}

			// 生成XML结构
			xmlEPG := router.GetXmlEPGData(chProgLists, backDays)
			xmlData, err := xml.MarshalIndent(xmlEPG, "", "  ")
			if err != nil {
				return fmt.Errorf("XML编码失败: %w", err)
			}

			// 确定输出文件名
			if outputFile == "" {
				outputFile = "epg.xml"
				if useGzip {
					outputFile += ".gz"
				}
			}

			// 写入文件
			file, err := os.Create(outputFile)
			if err != nil {
				return fmt.Errorf("创建文件失败: %w", err)
			}
			defer file.Close()

			var writer io.Writer = file
			if useGzip {
				gzWriter := gzip.NewWriter(file)
				defer gzWriter.Close()
				writer = gzWriter
			}

			// 写入XML头和数据
			if _, err := writer.Write([]byte(xml.Header)); err != nil {
				return fmt.Errorf("写入XML头失败: %w", err)
			}
			if _, err := writer.Write(xmlData); err != nil {
				return fmt.Errorf("写入XML内容失败: %w", err)
			}

			logger.Info("EPG导出成功", 
				zap.String("文件路径", outputFile),
				zap.Bool("压缩", useGzip),
			)
			return nil
		},
	}

	// 命令行参数
	epgCmd.Flags().StringVarP(&outputFile, "output", "o", "", "输出文件路径（默认epg.xml或epg.xml.gz）")
	epgCmd.Flags().IntVarP(&backDays, "back-days", "b", 0, "保留过去几天的节目单（默认全部）")
	epgCmd.Flags().BoolVarP(&useGzip, "gzip", "z", false, "启用Gzip压缩")

	return epgCmd
}