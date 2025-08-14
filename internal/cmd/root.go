package cmd

import (
	"flag"
	"fmt"
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

	if *cli {
		fmt.Println(flag.Args()[0])
		return api.RunTest(flag.Args()[0])
	}

	fmt.Println("Welcome to gurl")
	return nil
}
