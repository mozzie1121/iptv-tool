package cmds

import (
	"errors"
	"fmt"
	"iptv/internal/app/iptv"
	"iptv/internal/pkg/util"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

const keyFileName = "key.txt"

var authenticator string

func NewKeyCLI() *cobra.Command {
	keyCmd := &cobra.Command{
		Use:   "key",
		Short: "暴力破解IPTV的密钥",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 检查 Authenticator 长度是否小于 10
			if len(authenticator) < 10 {
				return errors.New("invalid authenticator")
			}

			// 获取当前目录
			currDir, err := util.GetCurrentAbPathByExecutable()
			if err != nil {
				return err
			}
			// 将结果写入文件
			filePath := path.Join(currDir, keyFileName)
			file, err := os.Create(filePath)
			if err != nil {
				return err
			}
			defer file.Close()

			// L()：获取全局logger
			logger := zap.L()

			var keys []string
			logger.Info("Start testing 00000000-99999999 all eight digits.")
			// 暴力破解从 00000000 到 99999999 的所有八位数字
			for x := 0; x < 100000000; x++ {
				key := fmt.Sprintf("%08d", x)

				// 每尝试 500,000 次输出一次进度
				if x%500000 == 0 {
					logger.Sugar().Infof("Tried to: -- %s --", key)
				}

				// 创建 3DES 解密器
				crypto := iptv.NewTripleDESCrypto(key)

				// 尝试解密 Authenticator
				decodedText, err := crypto.ECBDecrypt(authenticator)
				if err != nil {
					continue
				}

				// 解析解密后的文本
				infos := strings.Split(decodedText, "$")
				if len(infos) <= 7 {
					continue
				}

				// 写入文件
				var infoText = fmt.Sprintf("  Random: %s\n  EncryptToken: %s\n  UserID: %s\n  STBID: %s\n  IP: %s\n  MAC: %s\n  Reserved: %s\n  CU: %s",
					infos[0], infos[1], infos[2], infos[3], infos[4], infos[5], infos[6], infos[7])
				line := fmt.Sprintf("Find key: %s, Plaintext: %s\nDetails:\n%s\n\n", key, decodedText, infoText)
				logger.Info("Find a key.", zap.String("key", key))
				if _, err = file.WriteString(line); err != nil {
					logger.Error("Failed to write to file.", zap.Error(err))
					return err
				}

				keys = append(keys, key)
			}

			logger.Sugar().Infof("Crack complete! A total of %d keys were found, see file: %s.", len(keys), keyFileName)
			return nil
		},
	}

	keyCmd.Flags().StringVarP(&authenticator, "authenticator", "a", "", "请输入Authenticator值，可通过抓包获取。")

	// 必填参数
	_ = keyCmd.MarkFlagRequired("authenticator")

	return keyCmd
}
