package cmds

import (
	"iptv/internal/app/config"
	"iptv/internal/pkg/util"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	cfgFile string

	conf *config.Config
)

func init() {
	cobra.OnInitialize(initConfig)
}

func NewRootCLI() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "iptv",
		Short:         "IPTV工具",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	rootCmd.AddCommand(NewKeyCLI())
	rootCmd.AddCommand(NewChannelCLI())
	rootCmd.AddCommand(NewServeCLI())
	rootCmd.AddCommand(NewEPGCLI())
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "JSON配置文件的路径")

	return rootCmd
}

// initConfig 初始化配置文件
func initConfig() {
	var err error
	var fPath string

	if cfgFile != "" {
		// 使用命令参数中的配置文件
		fPath = cfgFile
	} else {
		cfgHome, err := util.GetCurrentAbPathByExecutable()
		cobra.CheckErr(err)

		fPath = filepath.Join(cfgHome, "config.yml")

		// 写入缺省配置文件
		if _, err = os.Stat(fPath); os.IsNotExist(err) {
			err = config.CreateDefaultCfg(fPath)
			cobra.CheckErr(err)
		}
	}

	// 读取配置文件
	conf, err = config.Load(fPath)
	cobra.CheckErr(err)
}
