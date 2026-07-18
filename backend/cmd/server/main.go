package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"tunnel-shm/internal/analyzer"
	"tunnel-shm/internal/api"
	"tunnel-shm/internal/collector"
	"tunnel-shm/internal/healthscore"
	"tunnel-shm/internal/model"
	"tunnel-shm/internal/store"
	"tunnel-shm/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/robfig/cron/v3"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // 生产环境应限制来源
	},
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("【系统-启动】隧道结构健康监测系统启动中...")

	// 数据库连接
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "tunnel")
	dbPass := getEnv("DB_PASS", "tunnel123")
	dbName := getEnv("DB_NAME", "tunnel_shm")
	serverPort := getEnv("SERVER_PORT", "8080")

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPass, dbHost, dbPort, dbName)

	ctx := context.Background()

	// 初始化数据库
	st, err := store.New(ctx, connStr)
	if err != nil {
		log.Fatalf("【系统-错误】数据库连接失败: %v", err)
	}
	defer st.Close()
	log.Println("【系统-数据库】连接成功")

	// 初始化表结构
	if err := st.InitSchema(ctx); err != nil {
		log.Printf("【系统-警告】初始化表结构失败（可能已存在）: %v", err)
	} else {
		log.Println("【系统-数据库】表结构初始化完成")
	}

	// 初始化WebSocket Hub
	hub := ws.NewHub()
	go hub.Run()
	log.Println("【系统-WS】WebSocket Hub已启动")

	// 初始化数据采集器
	col := collector.New(st, hub)

	// 初始化告警分析引擎
	anal := analyzer.New(st, hub, nil)

	// 初始化健康度评分调度器
	healthSched := healthscore.NewScheduler(st)
	healthSched.Start()
	defer healthSched.Stop()
	// 告警分析器在插入告警后异步触发对应断面重算
	anal.SetHealthScheduler(healthSched)

	// 初始化API处理器
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// CORS中间件
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		// 处理人信息由前端 axios 通过 X-User 头传入，
		// 必须在 CORS 的 Allow-Headers 中放行，否则浏览器预检请求会拒绝
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// 用户上下文中间件：从 X-User 头提取当前运维账号，
	// 写入 gin.Context（Key: "user"），供后续业务 handler 调用
	// model.GetCurrentUser(c) 统一读取。
	//
	// 设计要点：
	//   - X-User 缺失或为空时记为 AlertHandlerUnknown（"unknown"），
	//     不再像历史那样"完全不留痕"，让"按处理人统计"时有兜底分组。
	//   - 长度超 64 的账号会被截断并补日志，防御恶意客户端注入。
	//   - 这里不做鉴权（鉴权由前置网关/SSO 负责），仅做"识别"。
	//     后续若引入 SSO，回调/反向代理会在 X-User 写入已认证用户。
	r.Use(userContextMiddleware())

	// 注册API路由
	handler := api.NewHandler(st, r, anal)
	handler.RegisterRoutes(r)
	handler.RegisterHealthRoutes(healthSched)

	// 数据采集接口
	r.POST("/api/v1/collect", col.HandleCollectData)
	r.GET("/api/v1/health", col.HandleHealthCheck)

	// WebSocket接口
	r.GET("/ws", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("【WS-错误】升级连接失败: %v", err)
			return
		}

		client := ws.NewClient(conn, hub)
		hub.Register(client)
		go client.WritePump()
		go client.ReadPump(hub)
	})

	// 定时任务：
	//   - 速率告警分析：每 5 分钟的整 5 分钟位（xx:00, xx:05, xx:10...）执行
	//   - 存活感知扫描：每 5 分钟的整 10 分钟位（xx:00, xx:10, xx:20...）执行
	//   - 告警自动恢复：每 5 分钟的整 4 分钟位（xx:04, xx:09, xx:14...）执行
	// 三个任务错开 2~4 分钟，避免同一时刻大量 SQL 抢占连接池
	c := cron.New()
	c.AddFunc("*/5 * * * *", func() {
		anal.AnalyzeAllSensors(context.Background())
	})
	c.AddFunc("2-59/5 * * * *", func() {
		// 2/7/12/17/22/27/32/37/42/47/52/57 分触发
		// 与速率告警分析（0/5/10/15/...）错开至少 2 分钟
		anal.DetectOfflineSensors(context.Background())
	})
	c.AddFunc("4-59/5 * * * *", func() {
		// 4/9/14/19/24/29/34/39/44/49/54/59 分触发
		// 与上述两个任务错开 2~4 分钟
		anal.AutoResolveRecoveredAlerts(context.Background())
	})
	c.Start()
	log.Println("【系统-定时】告警分析定时任务已启动（每5分钟错峰）")
	log.Println("【系统-定时】存活感知定时任务已启动（每5分钟错峰）")
	log.Println("【系统-定时】告警自动恢复定时任务已启动（每5分钟错峰）")

	// 启动HTTP服务
	srv := &http.Server{
		Addr:    ":" + serverPort,
		Handler: r,
	}

	go func() {
		log.Printf("【系统-服务】HTTP服务启动在端口 %s", serverPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("【系统-错误】HTTP服务启动失败: %v", err)
		}
	}()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("【系统-关闭】正在关闭服务...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c.Stop()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("【系统-错误】服务关闭失败: %v", err)
	}
	log.Println("【系统-关闭】服务已安全关闭")
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// userContextMiddleware 从 HTTP 头提取当前用户，写入 gin.Context
//
// 优先顺序：
//   1. X-User 请求头（前端 axios 显式传入）
//   2. Authorization: Bearer <user>（兼容已有 token 场景，简单起见把整串作为 user）
//   3. 都缺失 / 全空白：记为 model.AlertHandlerUnknown
//
// 长度限制与 DB 字段（VARCHAR(64)）保持一致，超长会被截断并打 WARN 日志，
// 避免 SQL 报 "value too long for type character varying(64)"。
//
// 注意：此中间件只做"识别"，不参与鉴权。生产环境应在前置网关完成鉴权后
// 再写入 X-User；中间件内的任何分支都允许请求继续到 c.Next()。
func userContextMiddleware() gin.HandlerFunc {
	const maxUserLen = 64
	return func(c *gin.Context) {
		user := strings.TrimSpace(c.GetHeader("X-User"))
		if user == "" {
			// 兜底：尝试从 Authorization 头解析（仅做演示用，生产请用 JWT/SSO）
			if auth := c.GetHeader("Authorization"); auth != "" {
				user = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			}
		}
		if user == "" {
			user = model.AlertHandlerUnknown
		} else if len(user) > maxUserLen {
			log.Printf("【用户-上下文-警告】X-User 长度超限（%d -> %d）：%s...",
				len(user), maxUserLen, user[:maxUserLen])
			user = user[:maxUserLen]
		}
		c.Set("user", user)
		c.Next()
	}
}