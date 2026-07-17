package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"tunnel-shm/internal/analyzer"
	"tunnel-shm/internal/api"
	"tunnel-shm/internal/collector"
	"tunnel-shm/internal/healthscore"
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
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

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