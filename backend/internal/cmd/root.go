package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"

	"gurl/internal/api"
	"gurl/internal/web"
)

// Execute 执行根命令
func Execute() error {
	if len(os.Args) == 1 {
		// 开启web服务
		return web.StartServer()
	}
	// 定义命令行参数
	cli := flag.Bool("cli", false, "是否开启命令行模式")

	flag.Parse()

	if !*cli {
		fmt.Println("Welcome to gurl")
		return nil
	}

	// 有文件路径参数的情况
	if len(flag.Args()) == 1 {
		fmt.Println(flag.Args()[0])
		return api.RunTest(flag.Args()[0])
	}

	// 没有路径参数的情况，判断标准输入中是否有内容
	stat, err := os.Stdin.Stat()
	if err != nil {
		return fmt.Errorf("failed to get stdin stat: %w", err)
	}
	// 检查是否是管道或重定向输入 (通常是标准输入有内容的情况)
	if stat.Mode()&os.ModeNamedPipe != 0 || stat.Size() > 0 {
		// 从标准输入读取内容
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read from stdin: %w", err)
		}

		if len(bytes) > 0 {
			// 如果有内容，则执行API测试
			ret, err := api.RunTestFromStr(string(bytes))
			if err != nil {
				return fmt.Errorf("failed to run test: %w", err)
			}
			fmt.Println(ret)
			return nil
		}
	}

	return nil
}
