package web

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

// StartServer 启动Web服务器
func StartServer() error {
	router := gin.Default()

	// 设置静态文件目录
	router.Static("/static", "./internal/web/static")

	// 设置模板目录
	router.LoadHTMLGlob("./internal/web/templates/*")

	// 定义路由
	router.GET("/", func(c *gin.Context) {
		c.HTML(200, "index.html", gin.H{
			"title": "gurl - Request Testing Tool",
		})
	})

	// API路由
	api := router.Group("/api")
	{
		api.POST("/request", runRequest)
		api.GET("/examples", listExamples)
	}

	// 启动服务器
	fmt.Println("Starting web server on http://localhost:7777")
	return router.Run("127.0.0.1:7777")
}

// runRequest 处理请求执行
func runRequest(c *gin.Context) {
	// 实现请求执行逻辑
	c.JSON(200, gin.H{
		"status":  "success",
		"message": "Request processing logic will be implemented here",
	})
}

// listExamples 列出示例请求
func listExamples(c *gin.Context) {
	// 实现示例列表逻辑
	c.JSON(200, gin.H{
		"status": "success",
		"examples": []string{
			"example1.http",
			"example2.http",
		},
	})
}
