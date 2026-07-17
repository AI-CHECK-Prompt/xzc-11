package api

import (
	"net/http"
	"strconv"
	"time"

	"tunnel-shm/internal/healthscore"
	"tunnel-shm/internal/store"

	"github.com/gin-gonic/gin"
)

// HealthHandler 健康度 API
type HealthHandler struct {
	store     *store.Store
	scheduler *healthscore.Scheduler
}

func NewHealthHandler(st *store.Store, sch *healthscore.Scheduler) *HealthHandler {
	return &HealthHandler{store: st, scheduler: sch}
}

// RegisterHealthRoutes 注册健康度路由（由 Handler 暴露的 engine 路由组）
func (h *Handler) RegisterHealthRoutes(sch *healthscore.Scheduler) {
	hh := NewHealthHandler(h.store, sch)
	api := h.engine.Group("/api/v1")
	api.GET("/health-dashboard/rank", hh.GetRank)
	api.GET("/sections/:id/health", hh.GetSectionHealth)
	api.GET("/sections/:id/health/history", hh.GetSectionHealthHistory)
	api.POST("/sections/:id/health/recompute", hh.PostRecompute)
}

// GetRank 获取健康度排名总览
func (hh *HealthHandler) GetRank(c *gin.Context) {
	line := c.DefaultQuery("line_code", "3")
	items, err := hh.store.GetSectionHealthRank(c.Request.Context(), line)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 统计各等级数量
	gradeCount := map[string]int{
		"excellent": 0, "normal": 0, "attention": 0, "degraded": 0, "danger": 0,
	}
	for _, it := range items {
		gradeCount[it.Grade]++
	}
	c.JSON(http.StatusOK, gin.H{
		"data":        items,
		"total":       len(items),
		"grade_count": gradeCount,
		"line_code":   line,
	})
}

// GetSectionHealth 获取某断面的最新健康度评分（含明细）
func (hh *HealthHandler) GetSectionHealth(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}
	score, details, inters, err := hh.store.GetLatestSectionHealthScore(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "该断面尚无评分数据"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"score":       score,
		"details":     details,
		"intermediate": inters,
	})
}

// GetSectionHealthHistory 获取历史健康度曲线
func (hh *HealthHandler) GetSectionHealthHistory(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}
	now := time.Now()
	start := now.Add(-30 * 24 * time.Hour)
	end := now
	if s := c.Query("start"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			start = t
		}
	}
	if e := c.Query("end"); e != "" {
		if t, err := time.Parse(time.RFC3339, e); err == nil {
			end = t
		}
	}
	interval := c.DefaultQuery("interval", "1 day")

	pts, err := hh.store.GetHealthScoreHistoryAggregated(c.Request.Context(), id, start, end, interval)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":     pts,
		"total":    len(pts),
		"interval": interval,
		"start":    start,
		"end":      end,
	})
}

// PostRecompute 手动触发重算（管理用）
func (hh *HealthHandler) PostRecompute(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的断面ID"})
		return
	}
	hh.scheduler.EnqueueRecompute(id)
	c.JSON(http.StatusOK, gin.H{"message": "已加入重算队列"})
}
