package web

import (
	"embed"
	"fmt"
	"gurl/internal/api"
	"io"
	"io/fs"
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

//go:embed static/*
var staticFiles embed.FS

// StartServer 启动Web服务器
func StartServer() error {
	router := gin.Default()

	// 配置CORS
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"}
	config.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type"}
	router.Use(cors.New(config))

	// 创建静态文件系统
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("failed to create static file system: %w", err)
	}
	assetsFS, err := fs.Sub(staticFS, "assets")
	if err != nil {
		return fmt.Errorf("failed to create static file system: %w", err)
	}
	// 设置静态文件路由
	router.StaticFS("/assets", http.FS(assetsFS))

	router.StaticFileFS("/favicon.ico", "favicon.ico", http.FS(staticFS))

	// 将根路径也指向index.html
	router.GET("/", func(c *gin.Context) {
		http.ServeFileFS(c.Writer, c.Request, staticFS, "index.html")
	})

	// 对于SPA应用，将所有未匹配的路由都指向index.html
	router.NoRoute(func(c *gin.Context) {
		http.ServeFileFS(c.Writer, c.Request, staticFS, "index.html")
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
	defer c.Request.Body.Close()
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(500, gin.H{
			"status":  "error",
			"message": "Failed to read request body",
		})
		return
	}
	requestBody := string(body)

	res, err := api.RunTestFromStr(requestBody)
	if err != nil {
		c.JSON(500, gin.H{
			"status":  "error",
			"message": "Failed to read request body",
		})
		return
	}

	c.String(200, res)
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
